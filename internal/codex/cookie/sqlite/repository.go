package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"gorm.io/gorm"
)

type ValueCipher interface {
	Seal(codexkeyring.AEADPurpose, []byte, []byte) (codexkeyring.SealedValue, error)
	Open(codexkeyring.AEADPurpose, []byte, codexkeyring.SealedValue) ([]byte, error)
	Capabilities() codexkeyring.Capabilities
}

type Config struct {
	DB          *gorm.DB
	Cipher      ValueCipher
	BusyTimeout time.Duration
}

type Repository struct {
	database    *sql.DB
	cipher      ValueCipher
	busyTimeout time.Duration
	currentAEAD string
}

func Open(ctx context.Context, config Config) (*Repository, error) {
	if ctx == nil || config.DB == nil {
		return nil, &providercookie.ConfigurationError{Field: "database", Reason: "context and database are required"}
	}
	if isNil(config.Cipher) {
		return nil, &providercookie.ConfigurationError{Field: "cipher", Reason: "must be provided"}
	}
	if config.BusyTimeout < 0 {
		return nil, &providercookie.ConfigurationError{Field: "busy_timeout", Reason: "must not be negative"}
	}
	if config.BusyTimeout == 0 {
		config.BusyTimeout = DefaultBusyTimeout
	}
	if err := ValidateSchema(ctx, config.DB); err != nil {
		return nil, err
	}
	capabilities := config.Cipher.Capabilities()
	if capabilities.AEADCurrent == "" {
		return nil, &providercookie.PersistenceError{
			Kind:      providercookie.PersistenceCrypto,
			Operation: "open_repository",
			Cause:     errors.New("current AEAD version is unavailable"),
		}
	}
	database, err := config.DB.DB()
	if err != nil {
		return nil, classifyDatabaseError("open_repository", err)
	}
	return &Repository{
		database:    database,
		cipher:      config.Cipher,
		busyTimeout: config.BusyTimeout,
		currentAEAD: capabilities.AEADCurrent,
	}, nil
}

func (r *Repository) UseBinding(ctx context.Context, lookup providercookie.BindingLookup) (providercookie.BindingUse, error) {
	if ctx == nil {
		return providercookie.BindingUse{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if err := lookup.Policy.Validate(); err != nil {
		return providercookie.BindingUse{}, err
	}
	if err := validateLookup(lookup); err != nil {
		return providercookie.BindingUse{}, err
	}
	var result providercookie.BindingUse
	err := withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		records, err := findBindings(ctx, connection, lookup.HandleDigests)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			result.Disposition = providercookie.BindingUnknown
			return nil
		}
		if len(records) != 1 {
			return corruptError("lookup_binding", errors.New("multiple handle digests matched one opaque handle"))
		}
		record := records[0]
		result.Record = record
		owners := make(map[codexidentity.ClientScope]struct{}, len(lookup.ClientScopes))
		for _, scope := range lookup.ClientScopes {
			owners[scope] = struct{}{}
		}
		if _, owned := owners[record.ClientScope]; !owned {
			result.Disposition = providercookie.BindingOwnerMismatch
			return nil
		}
		at := lookup.At.UTC()
		if !record.IdleExpiresAt.After(at) || !record.AbsoluteExpiresAt.After(at) {
			result.Disposition = providercookie.BindingExpired
			return nil
		}
		remainingExpiry := record.IdleExpiresAt
		if record.AbsoluteExpiresAt.Before(remainingExpiry) {
			remainingExpiry = record.AbsoluteExpiresAt
		}
		result.Refresh = remainingExpiry.Sub(at) <= lookup.Policy.HandleRefreshWindow
		var effectiveAccessMS, effectiveIdleMS int64
		if err := connection.QueryRowContext(ctx,
			"UPDATE "+handlesTable+` SET
				last_access_at_ms = MAX(last_access_at_ms, ?),
				idle_expires_at_ms = MIN(absolute_expires_at_ms,
					MAX(idle_expires_at_ms, MAX(last_access_at_ms, ?) + ?))
			WHERE handle_key_version = ? AND handle_digest = ?
			RETURNING last_access_at_ms, idle_expires_at_ms`,
			toMillis(at), toMillis(at), lookup.Policy.HandleIdleTTL.Milliseconds(),
			record.HandleDigest.Version, record.HandleDigest.Sum[:],
		).Scan(&effectiveAccessMS, &effectiveIdleMS); err != nil {
			return classifyDatabaseError("touch_binding", err)
		}
		record.LastAccessAt = fromMillis(effectiveAccessMS)
		record.IdleExpiresAt = fromMillis(effectiveIdleMS)
		result.Record = record
		result.Disposition = providercookie.BindingValid
		return nil
	})
	return result, err
}

func (r *Repository) Load(ctx context.Context, scope providercookie.CookieScope, at time.Time) (providercookie.Snapshot, error) {
	if ctx == nil {
		return providercookie.Snapshot{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	authority, err := encodedAuthority(scope)
	if err != nil {
		return providercookie.Snapshot{}, err
	}
	var bindingCount int
	if err := r.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+handlesTable+" WHERE jar_id = ?", scope.JarID().Bytes()).Scan(&bindingCount); err != nil {
		return providercookie.Snapshot{}, classifyDatabaseError("load_binding", err)
	}
	if bindingCount != 1 {
		return providercookie.Snapshot{}, storageError("load_binding", providercookie.ErrBindingNotFound)
	}
	rows, err := r.database.QueryContext(ctx, "SELECT "+entryColumns+" FROM "+entriesTable+` 
		WHERE jar_id = ? AND authority = ? AND expires_at_ms > ?
		ORDER BY cookie_name, cookie_domain, cookie_path`, scope.JarID().Bytes(), authority, toMillis(at))
	if err != nil {
		return providercookie.Snapshot{}, classifyDatabaseError("load_cookies", err)
	}
	defer rows.Close()
	cookies := make([]providercookie.StoredCookie, 0)
	for rows.Next() {
		row, err := scanEntry(rows)
		if err != nil {
			return providercookie.Snapshot{}, err
		}
		cookie, err := r.decryptEntry(scope, row)
		if err != nil {
			return providercookie.Snapshot{}, err
		}
		cookies = append(cookies, cookie)
	}
	if err := rows.Err(); err != nil {
		return providercookie.Snapshot{}, classifyDatabaseError("load_cookies", err)
	}
	return providercookie.NewSnapshot(scope, cookies)
}

func (r *Repository) Touch(ctx context.Context, scope providercookie.CookieScope, keys []providercookie.CookieKey, at time.Time) error {
	if ctx == nil {
		return &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	authority, err := encodedAuthority(scope)
	if err != nil {
		return err
	}
	return withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		if err := requireJar(ctx, connection, scope.JarID()); err != nil {
			return err
		}
		touched := int64(0)
		for _, key := range uniqueKeys(keys) {
			result, err := connection.ExecContext(ctx, "UPDATE "+entriesTable+` SET last_access_at_ms = MAX(last_access_at_ms, ?)
				WHERE jar_id = ? AND authority = ? AND cookie_name = ? AND cookie_domain = ? AND cookie_path = ?`,
				toMillis(at), scope.JarID().Bytes(), authority, key.Name(), key.Domain(), key.Path(),
			)
			if err != nil {
				return classifyDatabaseError("touch_cookies", err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				return classifyDatabaseError("touch_cookies", err)
			}
			touched += count
		}
		if touched > 0 {
			if _, err := connection.ExecContext(ctx, "UPDATE "+authoritiesTable+` SET last_access_at_ms = MAX(last_access_at_ms, ?), unreachable_since_ms = NULL
				WHERE jar_id = ? AND authority = ?`, toMillis(at), scope.JarID().Bytes(), authority); err != nil {
				return classifyDatabaseError("touch_authority", err)
			}
		}
		return nil
	})
}

func RequiredHMACVersions(ctx context.Context, db *gorm.DB) ([]string, error) {
	if err := ValidateSchema(ctx, db); err != nil {
		return nil, err
	}
	var versions []string
	if err := db.WithContext(ctx).Raw("SELECT handle_key_version AS version FROM " + handlesTable + ` UNION
		SELECT client_scope_key_version AS version FROM ` + handlesTable + " ORDER BY version").Scan(&versions).Error; err != nil {
		return nil, classifyDatabaseError("required_hmac_versions", err)
	}
	return versions, nil
}

func RequiredAEADVersions(ctx context.Context, db *gorm.DB) ([]string, error) {
	if err := ValidateSchema(ctx, db); err != nil {
		return nil, err
	}
	var versions []string
	if err := db.WithContext(ctx).Raw("SELECT DISTINCT value_key_version FROM " + entriesTable + " ORDER BY value_key_version").Scan(&versions).Error; err != nil {
		return nil, classifyDatabaseError("required_aead_versions", err)
	}
	return versions, nil
}

func (r *Repository) RequiredHMACVersions(ctx context.Context) ([]string, error) {
	rows, err := r.database.QueryContext(ctx, "SELECT handle_key_version FROM "+handlesTable+` UNION
		SELECT client_scope_key_version FROM `+handlesTable+" ORDER BY 1")
	if err != nil {
		return nil, classifyDatabaseError("required_hmac_versions", err)
	}
	return scanVersions(rows)
}

func (r *Repository) RequiredAEADVersions(ctx context.Context) ([]string, error) {
	rows, err := r.database.QueryContext(ctx, "SELECT DISTINCT value_key_version FROM "+entriesTable+" ORDER BY value_key_version")
	if err != nil {
		return nil, classifyDatabaseError("required_aead_versions", err)
	}
	return scanVersions(rows)
}

func scanVersions(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	versions := make([]string, 0)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, classifyDatabaseError("scan_key_versions", err)
		}
		if version == "" {
			return nil, corruptError("scan_key_versions", errors.New("persisted key version is empty"))
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("scan_key_versions", err)
	}
	return versions, nil
}

func validateLookup(lookup providercookie.BindingLookup) error {
	if lookup.At.IsZero() || len(lookup.HandleDigests) == 0 || len(lookup.ClientScopes) == 0 {
		return &providercookie.ConfigurationError{Field: "binding_lookup", Reason: "time, handle digests, and client scopes are required"}
	}
	seenDigests := make(map[codexkeyring.Digest]struct{}, len(lookup.HandleDigests))
	for _, digest := range lookup.HandleDigests {
		if digest.Version == "" {
			return &providercookie.ConfigurationError{Field: "handle_digest", Reason: "key version is required"}
		}
		if _, exists := seenDigests[digest]; exists {
			return &providercookie.ConfigurationError{Field: "handle_digest", Reason: "duplicate lookup digest"}
		}
		seenDigests[digest] = struct{}{}
	}
	for _, scope := range lookup.ClientScopes {
		if _, err := scope.MarshalBinary(); err != nil {
			return &providercookie.ConfigurationError{Field: "client_scope", Reason: "lookup scope is invalid"}
		}
	}
	return nil
}

func validateBinding(record providercookie.BindingRecord) error {
	if record.HandleDigest.Version == "" {
		return &providercookie.ConfigurationError{Field: "handle_digest", Reason: "key version is required"}
	}
	if record.JarID == (providercookie.JarID{}) {
		return &providercookie.ConfigurationError{Field: "jar_id", Reason: "must be initialized"}
	}
	if _, err := record.ClientScope.MarshalBinary(); err != nil {
		return &providercookie.ConfigurationError{Field: "client_scope", Reason: "must be initialized"}
	}
	if record.CreatedAt.IsZero() || record.LastAccessAt.Before(record.CreatedAt) ||
		!record.IdleExpiresAt.After(record.LastAccessAt) || !record.AbsoluteExpiresAt.After(record.CreatedAt) {
		return &providercookie.ConfigurationError{Field: "binding_times", Reason: "timestamps are inconsistent"}
	}
	return nil
}

const bindingColumns = `handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest,
	created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms`

func findBindings(ctx context.Context, connection *sql.Conn, digests []codexkeyring.Digest) ([]providercookie.BindingRecord, error) {
	predicates := make([]string, 0, len(digests))
	arguments := make([]any, 0, len(digests)*2)
	for _, digest := range digests {
		predicates = append(predicates, "(handle_key_version = ? AND handle_digest = ?)")
		arguments = append(arguments, digest.Version, digest.Sum[:])
	}
	return queryBindings(ctx, connection, strings.Join(predicates, " OR "), arguments)
}

func queryBindings(
	ctx context.Context,
	connection *sql.Conn,
	predicate string,
	arguments []any,
) ([]providercookie.BindingRecord, error) {
	rows, err := connection.QueryContext(ctx, "SELECT "+bindingColumns+" FROM "+handlesTable+" WHERE "+predicate, arguments...)
	if err != nil {
		return nil, classifyDatabaseError("lookup_binding", err)
	}
	defer rows.Close()
	result := make([]providercookie.BindingRecord, 0, 1)
	for rows.Next() {
		var version, clientVersion string
		var digestBytes, jarBytes, clientDigest []byte
		var created, accessed, idle, absolute int64
		if err := rows.Scan(&version, &digestBytes, &jarBytes, &clientVersion, &clientDigest, &created, &accessed, &idle, &absolute); err != nil {
			return nil, classifyDatabaseError("scan_binding", err)
		}
		if len(digestBytes) != codexidentity.DigestSize || len(clientDigest) != codexidentity.DigestSize {
			return nil, corruptError("scan_binding", errors.New("persisted digest length is invalid"))
		}
		var digest, ownerDigest [codexidentity.DigestSize]byte
		copy(digest[:], digestBytes)
		copy(ownerDigest[:], clientDigest)
		jarID, err := providercookie.JarIDFromBytes(jarBytes)
		if err != nil {
			return nil, corruptError("scan_binding", err)
		}
		owner, err := codexidentity.ClientScopeFromDigest(clientVersion, ownerDigest)
		if err != nil {
			return nil, corruptError("scan_binding", err)
		}
		record := providercookie.BindingRecord{
			HandleDigest:      codexkeyring.Digest{Version: version, Sum: digest},
			JarID:             jarID,
			ClientScope:       owner,
			CreatedAt:         fromMillis(created),
			LastAccessAt:      fromMillis(accessed),
			IdleExpiresAt:     fromMillis(idle),
			AbsoluteExpiresAt: fromMillis(absolute),
		}
		if err := validateBinding(record); err != nil {
			return nil, corruptError("scan_binding", err)
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("lookup_binding", err)
	}
	return result, nil
}

func requireJar(ctx context.Context, connection *sql.Conn, jarID providercookie.JarID) error {
	var count int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+handlesTable+" WHERE jar_id = ?", jarID.Bytes()).Scan(&count); err != nil {
		return classifyDatabaseError("require_jar", err)
	}
	if count != 1 {
		return storageError("require_jar", providercookie.ErrBindingNotFound)
	}
	return nil
}

func uniqueKeys(keys []providercookie.CookieKey) []providercookie.CookieKey {
	seen := make(map[providercookie.CookieKey]struct{}, len(keys))
	result := make([]providercookie.CookieKey, 0, len(keys))
	for _, key := range keys {
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name() != result[j].Name() {
			return result[i].Name() < result[j].Name()
		}
		if result[i].Domain() != result[j].Domain() {
			return result[i].Domain() < result[j].Domain()
		}
		return result[i].Path() < result[j].Path()
	})
	return result
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func toMillis(value time.Time) int64   { return value.UTC().UnixMilli() }
func fromMillis(value int64) time.Time { return time.UnixMilli(value).UTC() }
