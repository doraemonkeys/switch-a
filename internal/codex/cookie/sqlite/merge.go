package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func (r *Repository) Merge(
	ctx context.Context,
	scope providercookie.CookieScope,
	mutations []providercookie.Mutation,
	at time.Time,
	policy providercookie.Policy,
) (providercookie.MergeResult, error) {
	if ctx == nil {
		return providercookie.MergeResult{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if err := policy.Validate(); err != nil {
		return providercookie.MergeResult{}, err
	}
	authority, err := encodedAuthority(scope)
	if err != nil {
		return providercookie.MergeResult{}, err
	}
	overlay, err := providercookie.NewOverlay(scope, policy)
	if err != nil {
		return providercookie.MergeResult{}, err
	}
	if err := overlay.ApplyBatch(scope, mutations); err != nil {
		return providercookie.MergeResult{}, err
	}
	changes, err := overlay.Changes(scope)
	if err != nil {
		return providercookie.MergeResult{}, err
	}

	var result providercookie.MergeResult
	err = withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		merged, mergeErr := r.mergeTransaction(ctx, connection, scope, authority, changes, at, policy)
		if mergeErr == nil {
			result = merged
		}
		return mergeErr
	})
	return result, err
}

func (r *Repository) mergeTransaction(
	ctx context.Context,
	connection *sql.Conn,
	scope providercookie.CookieScope,
	authority []byte,
	changes []providercookie.Mutation,
	at time.Time,
	policy providercookie.Policy,
) (providercookie.MergeResult, error) {
	if _, err := cleanupStale(ctx, connection, at, policy.OrphanAuthorityGrace); err != nil {
		return providercookie.MergeResult{}, err
	}
	if err := requireJar(ctx, connection, scope.JarID()); err != nil {
		return providercookie.MergeResult{}, err
	}
	exists, err := authorityExists(ctx, connection, scope, authority)
	if err != nil {
		return providercookie.MergeResult{}, err
	}
	result, exists, err := r.applyMutations(ctx, connection, scope, authority, changes, at, exists)
	if err != nil {
		return providercookie.MergeResult{}, err
	}
	if exists {
		result.Reencrypted, err = r.reencryptLegacy(ctx, connection, scope, authority)
		if err != nil {
			return providercookie.MergeResult{}, err
		}
	}
	result.Evicted, err = enforceJarCapacity(ctx, connection, scope.JarID(), authority, policy)
	if err != nil {
		return providercookie.MergeResult{}, err
	}
	if err := enforceGlobalCapacity(ctx, connection, policy.MaxCookieEntriesGlobal); err != nil {
		return providercookie.MergeResult{}, err
	}
	return result, nil
}

func (r *Repository) applyMutations(
	ctx context.Context,
	connection *sql.Conn,
	scope providercookie.CookieScope,
	authority []byte,
	changes []providercookie.Mutation,
	at time.Time,
	authorityExists bool,
) (providercookie.MergeResult, bool, error) {
	result := providercookie.MergeResult{}
	for _, mutation := range changes {
		key := mutation.Key()
		cookie, upsert := mutation.Cookie()
		if !upsert || cookie.Expired(at) {
			deleted, err := deleteCookie(ctx, connection, scope, authority, key)
			if err != nil {
				return providercookie.MergeResult{}, authorityExists, err
			}
			result.Deleted += deleted
			continue
		}
		if !authorityExists {
			if err := upsertAuthority(ctx, connection, scope, authority, at); err != nil {
				return providercookie.MergeResult{}, false, err
			}
			authorityExists = true
		}
		sealed, err := r.sealCookie(scope, cookie)
		if err != nil {
			return providercookie.MergeResult{}, authorityExists, err
		}
		if err := upsertCookie(ctx, connection, scope, authority, cookie, sealed, at); err != nil {
			return providercookie.MergeResult{}, authorityExists, err
		}
		result.Upserted++
	}
	return result, authorityExists, nil
}

func enforceGlobalCapacity(ctx context.Context, connection *sql.Conn, maximum int) error {
	var globalEntries int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+entriesTable).Scan(&globalEntries); err != nil {
		return classifyDatabaseError("count_global_cookies", err)
	}
	if globalEntries > maximum {
		return &providercookie.LimitError{
			Limit:  providercookie.LimitGlobalEntries,
			Max:    maximum,
			Actual: globalEntries,
		}
	}
	return nil
}

func authorityExists(ctx context.Context, connection *sql.Conn, scope providercookie.CookieScope, authority []byte) (bool, error) {
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+authoritiesTable+" WHERE jar_id = ? AND authority = ?", scope.JarID().Bytes(), authority).Scan(&count); err != nil {
		return false, classifyDatabaseError("find_authority", err)
	}
	if count > 1 {
		return false, corruptError("find_authority", errors.New("duplicate authority rows"))
	}
	return count == 1, nil
}

func upsertAuthority(ctx context.Context, connection *sql.Conn, scope providercookie.CookieScope, authority []byte, at time.Time) error {
	_, err := connection.ExecContext(ctx, "INSERT INTO "+authoritiesTable+` (
		jar_id, authority, created_at_ms, last_access_at_ms, unreachable_since_ms
	) VALUES (?, ?, ?, ?, NULL)
	ON CONFLICT(jar_id, authority) DO UPDATE SET
		last_access_at_ms = MAX(last_access_at_ms, excluded.last_access_at_ms),
		unreachable_since_ms = NULL`, scope.JarID().Bytes(), authority, toMillis(at), toMillis(at))
	if err != nil {
		return classifyDatabaseError("upsert_authority", err)
	}
	return nil
}

func upsertCookie(
	ctx context.Context,
	connection *sql.Conn,
	scope providercookie.CookieScope,
	authority []byte,
	cookie providercookie.StoredCookie,
	sealed codexkeyring.SealedValue,
	at time.Time,
) error {
	key := cookie.Key()
	_, err := connection.ExecContext(ctx, "INSERT INTO "+entriesTable+` (
		jar_id, authority, cookie_name, cookie_domain, cookie_path,
		value_key_version, value_nonce, value_ciphertext,
		host_only, secure, http_only, quoted, session, same_site,
		expires_at_ms, created_at_ms, last_access_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(jar_id, authority, cookie_name, cookie_domain, cookie_path) DO UPDATE SET
		value_key_version = excluded.value_key_version,
		value_nonce = excluded.value_nonce,
		value_ciphertext = excluded.value_ciphertext,
		host_only = excluded.host_only,
		secure = excluded.secure,
		http_only = excluded.http_only,
		quoted = excluded.quoted,
		session = excluded.session,
		same_site = excluded.same_site,
		expires_at_ms = excluded.expires_at_ms,
		last_access_at_ms = MAX(last_access_at_ms, excluded.last_access_at_ms)`,
		scope.JarID().Bytes(),
		authority,
		key.Name(),
		key.Domain(),
		key.Path(),
		sealed.Version,
		sealed.Nonce,
		sealed.Ciphertext,
		boolInt(cookie.HostOnly()),
		boolInt(cookie.Secure()),
		boolInt(cookie.HTTPOnly()),
		boolInt(cookie.Quoted()),
		boolInt(cookie.Session()),
		int(cookie.SameSite()),
		toMillis(cookie.ExpiresAt()),
		toMillis(cookie.CreatedAt()),
		toMillis(at),
	)
	if err != nil {
		return classifyDatabaseError("upsert_cookie", err)
	}
	return nil
}

func deleteCookie(
	ctx context.Context,
	connection *sql.Conn,
	scope providercookie.CookieScope,
	authority []byte,
	key providercookie.CookieKey,
) (int, error) {
	result, err := connection.ExecContext(ctx, "DELETE FROM "+entriesTable+`
		WHERE jar_id = ? AND authority = ? AND cookie_name = ? AND cookie_domain = ? AND cookie_path = ?`,
		scope.JarID().Bytes(), authority, key.Name(), key.Domain(), key.Path())
	if err != nil {
		return 0, classifyDatabaseError("delete_cookie", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDatabaseError("delete_cookie", err)
	}
	return int(count), nil
}

func (r *Repository) reencryptLegacy(
	ctx context.Context,
	connection *sql.Conn,
	scope providercookie.CookieScope,
	authority []byte,
) (int, error) {
	rows, err := connection.QueryContext(ctx, "SELECT "+entryColumns+" FROM "+entriesTable+`
		WHERE jar_id = ? AND authority = ? AND value_key_version <> ?
		ORDER BY cookie_name, cookie_domain, cookie_path`, scope.JarID().Bytes(), authority, r.currentAEAD)
	if err != nil {
		return 0, classifyDatabaseError("load_legacy_cookies", err)
	}
	legacy := make([]entryRow, 0)
	for rows.Next() {
		row, err := scanEntry(rows)
		if err != nil {
			_ = rows.Close()
			return 0, err
		}
		legacy = append(legacy, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDatabaseError("load_legacy_cookies", err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDatabaseError("load_legacy_cookies", err)
	}
	for _, row := range legacy {
		cookie, err := r.decryptEntry(scope, row)
		if err != nil {
			return 0, err
		}
		sealed, err := r.sealCookie(scope, cookie)
		if err != nil {
			return 0, err
		}
		key := cookie.Key()
		if _, err := connection.ExecContext(ctx, "UPDATE "+entriesTable+` SET
			value_key_version = ?, value_nonce = ?, value_ciphertext = ?
			WHERE jar_id = ? AND authority = ? AND cookie_name = ? AND cookie_domain = ? AND cookie_path = ?`,
			sealed.Version, sealed.Nonce, sealed.Ciphertext,
			scope.JarID().Bytes(), authority, key.Name(), key.Domain(), key.Path(),
		); err != nil {
			return 0, classifyDatabaseError("reencrypt_cookie", err)
		}
	}
	return len(legacy), nil
}

func enforceJarCapacity(
	ctx context.Context,
	connection *sql.Conn,
	jarID providercookie.JarID,
	currentAuthority []byte,
	policy providercookie.Policy,
) (int, error) {
	evicted := 0
	count, err := evictEntries(ctx, connection,
		"jar_id = ? AND authority = ?", []any{jarID.Bytes(), currentAuthority}, policy.MaxCookiesPerAuthority)
	if err != nil {
		return 0, err
	}
	evicted += count
	count, err = evictEntries(ctx, connection, "jar_id = ?", []any{jarID.Bytes()}, policy.MaxCookiesPerJar)
	if err != nil {
		return 0, err
	}
	evicted += count
	if _, err := deleteEmptyAuthorities(ctx, connection, jarID.Bytes()); err != nil {
		return 0, err
	}
	count, err = evictAuthorities(ctx, connection, jarID, policy.MaxAuthoritiesPerJar)
	if err != nil {
		return 0, err
	}
	evicted += count
	return evicted, nil
}

func evictEntries(ctx context.Context, connection *sql.Conn, predicate string, args []any, maximum int) (int, error) {
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+entriesTable+" WHERE "+predicate, args...).Scan(&count); err != nil {
		return 0, classifyDatabaseError("count_cookie_capacity", err)
	}
	overflow := count - maximum
	if overflow <= 0 {
		return 0, nil
	}
	queryArgs := append(append([]any(nil), args...), overflow)
	rows, err := connection.QueryContext(ctx, "SELECT jar_id, authority, cookie_name, cookie_domain, cookie_path FROM "+entriesTable+" WHERE "+predicate+`
		ORDER BY last_access_at_ms ASC, created_at_ms ASC, cookie_name ASC, cookie_domain ASC, cookie_path ASC
		LIMIT ?`, queryArgs...)
	if err != nil {
		return 0, classifyDatabaseError("select_cookie_evictions", err)
	}
	type candidate struct {
		jar, authority     []byte
		name, domain, path string
	}
	candidates := make([]candidate, 0, overflow)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.jar, &item.authority, &item.name, &item.domain, &item.path); err != nil {
			_ = rows.Close()
			return 0, classifyDatabaseError("select_cookie_evictions", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDatabaseError("select_cookie_evictions", err)
	}
	for _, item := range candidates {
		if _, err := connection.ExecContext(ctx, "DELETE FROM "+entriesTable+` WHERE jar_id = ? AND authority = ?
			AND cookie_name = ? AND cookie_domain = ? AND cookie_path = ?`, item.jar, item.authority, item.name, item.domain, item.path); err != nil {
			return 0, classifyDatabaseError("evict_cookie", err)
		}
	}
	return len(candidates), nil
}

func evictAuthorities(ctx context.Context, connection *sql.Conn, jarID providercookie.JarID, maximum int) (int, error) {
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+authoritiesTable+" WHERE jar_id = ?", jarID.Bytes()).Scan(&count); err != nil {
		return 0, classifyDatabaseError("count_authority_capacity", err)
	}
	overflow := count - maximum
	if overflow <= 0 {
		return 0, nil
	}
	rows, err := connection.QueryContext(ctx, "SELECT authority FROM "+authoritiesTable+`
		WHERE jar_id = ? ORDER BY last_access_at_ms ASC, created_at_ms ASC, hex(authority) ASC LIMIT ?`, jarID.Bytes(), overflow)
	if err != nil {
		return 0, classifyDatabaseError("select_authority_evictions", err)
	}
	authorities := make([][]byte, 0, overflow)
	for rows.Next() {
		var authority []byte
		if err := rows.Scan(&authority); err != nil {
			_ = rows.Close()
			return 0, classifyDatabaseError("select_authority_evictions", err)
		}
		authorities = append(authorities, authority)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDatabaseError("select_authority_evictions", err)
	}
	for _, authority := range authorities {
		if _, err := deleteAuthority(ctx, connection, jarID.Bytes(), authority); err != nil {
			return 0, err
		}
	}
	return len(authorities), nil
}
