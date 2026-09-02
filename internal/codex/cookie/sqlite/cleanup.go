package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

func (r *Repository) Cleanup(ctx context.Context, request providercookie.CleanupRequest) (providercookie.CleanupResult, error) {
	if ctx == nil {
		return providercookie.CleanupResult{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if request.At.IsZero() {
		return providercookie.CleanupResult{}, &providercookie.ConfigurationError{Field: "cleanup_time", Reason: "must be provided"}
	}
	if err := request.Policy.Validate(); err != nil {
		return providercookie.CleanupResult{}, err
	}
	reachable := make([][]byte, 0, len(request.ReachableAuthorities))
	for _, authority := range request.ReachableAuthorities {
		encoded, err := authority.MarshalBinary()
		if err != nil {
			return providercookie.CleanupResult{}, &providercookie.ConfigurationError{Field: "reachable_authorities", Reason: "contains an invalid authority"}
		}
		reachable = append(reachable, encoded)
	}

	var result providercookie.CleanupResult
	err := withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		cleaned, cleanupErr := cleanupTransaction(ctx, connection, request, reachable)
		if cleanupErr == nil {
			result = cleaned
		}
		return cleanupErr
	})
	return result, err
}

type authorityReachabilityRow struct {
	jar         []byte
	authority   []byte
	unreachable sql.NullInt64
}

func cleanupTransaction(
	ctx context.Context,
	connection *sql.Conn,
	request providercookie.CleanupRequest,
	reachable [][]byte,
) (providercookie.CleanupResult, error) {
	result, err := cleanupStale(ctx, connection, request.At, request.Policy.OrphanAuthorityGrace)
	if err != nil {
		return providercookie.CleanupResult{}, err
	}
	items, err := loadAuthorityReachability(ctx, connection)
	if err != nil {
		return providercookie.CleanupResult{}, err
	}
	if err := applyAuthorityReachability(ctx, connection, items, reachable, request.At); err != nil {
		return providercookie.CleanupResult{}, err
	}
	final, err := cleanupStale(ctx, connection, request.At, request.Policy.OrphanAuthorityGrace)
	if err != nil {
		return providercookie.CleanupResult{}, err
	}
	result.ExpiredBindings += final.ExpiredBindings
	result.ExpiredCookies += final.ExpiredCookies
	result.OrphanAuthorities += final.OrphanAuthorities
	result.EmptyAuthorities += final.EmptyAuthorities
	return result, nil
}

func loadAuthorityReachability(ctx context.Context, connection *sql.Conn) ([]authorityReachabilityRow, error) {
	rows, err := connection.QueryContext(ctx, "SELECT jar_id, authority, unreachable_since_ms FROM "+authoritiesTable)
	if err != nil {
		return nil, classifyDatabaseError("load_authority_reachability", err)
	}
	items := make([]authorityReachabilityRow, 0)
	for rows.Next() {
		var item authorityReachabilityRow
		if err := rows.Scan(&item.jar, &item.authority, &item.unreachable); err != nil {
			_ = rows.Close()
			return nil, classifyDatabaseError("load_authority_reachability", err)
		}
		if len(item.jar) != providercookie.JarIDEntropyBytes || len(item.authority) == 0 {
			_ = rows.Close()
			return nil, corruptError("load_authority_reachability", errors.New("authority identity is invalid"))
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, classifyDatabaseError("load_authority_reachability", err)
	}
	if err := rows.Close(); err != nil {
		return nil, classifyDatabaseError("load_authority_reachability", err)
	}
	return items, nil
}

func applyAuthorityReachability(
	ctx context.Context,
	connection *sql.Conn,
	items []authorityReachabilityRow,
	reachable [][]byte,
	at time.Time,
) error {
	for _, item := range items {
		if containsBytes(reachable, item.authority) {
			if item.unreachable.Valid {
				if _, err := connection.ExecContext(ctx, "UPDATE "+authoritiesTable+" SET unreachable_since_ms = NULL WHERE jar_id = ? AND authority = ?", item.jar, item.authority); err != nil {
					return classifyDatabaseError("mark_authority_reachable", err)
				}
			}
			continue
		}
		if !item.unreachable.Valid {
			if _, err := connection.ExecContext(ctx, "UPDATE "+authoritiesTable+" SET unreachable_since_ms = ? WHERE jar_id = ? AND authority = ?", toMillis(at), item.jar, item.authority); err != nil {
				return classifyDatabaseError("mark_authority_unreachable", err)
			}
		}
	}
	return nil
}

func cleanupStale(
	ctx context.Context,
	connection *sql.Conn,
	at time.Time,
	orphanGrace time.Duration,
) (providercookie.CleanupResult, error) {
	var result providercookie.CleanupResult
	expired, err := connection.ExecContext(ctx, "DELETE FROM "+entriesTable+" WHERE expires_at_ms <= ?", toMillis(at))
	if err != nil {
		return result, classifyDatabaseError("delete_expired_cookies", err)
	}
	if count, err := expired.RowsAffected(); err == nil {
		result.ExpiredCookies = int(count)
	} else {
		return result, classifyDatabaseError("delete_expired_cookies", err)
	}

	rows, err := connection.QueryContext(ctx, "SELECT jar_id FROM "+handlesTable+" WHERE idle_expires_at_ms <= ? OR absolute_expires_at_ms <= ?", toMillis(at), toMillis(at))
	if err != nil {
		return result, classifyDatabaseError("find_expired_bindings", err)
	}
	jars := make([][]byte, 0)
	for rows.Next() {
		var jar []byte
		if err := rows.Scan(&jar); err != nil {
			_ = rows.Close()
			return result, classifyDatabaseError("find_expired_bindings", err)
		}
		if len(jar) != providercookie.JarIDEntropyBytes {
			_ = rows.Close()
			return result, corruptError("find_expired_bindings", errors.New("expired binding JarID is invalid"))
		}
		jars = append(jars, jar)
	}
	if err := rows.Close(); err != nil {
		return result, classifyDatabaseError("find_expired_bindings", err)
	}
	for _, jar := range jars {
		if err := deleteJar(ctx, connection, jar); err != nil {
			return result, err
		}
		result.ExpiredBindings++
	}

	cutoff := at.Add(-orphanGrace)
	rows, err = connection.QueryContext(ctx, "SELECT jar_id, authority FROM "+authoritiesTable+" WHERE unreachable_since_ms IS NOT NULL AND unreachable_since_ms <= ?", toMillis(cutoff))
	if err != nil {
		return result, classifyDatabaseError("find_orphan_authorities", err)
	}
	type orphan struct{ jar, authority []byte }
	orphans := make([]orphan, 0)
	for rows.Next() {
		var item orphan
		if err := rows.Scan(&item.jar, &item.authority); err != nil {
			_ = rows.Close()
			return result, classifyDatabaseError("find_orphan_authorities", err)
		}
		orphans = append(orphans, item)
	}
	if err := rows.Close(); err != nil {
		return result, classifyDatabaseError("find_orphan_authorities", err)
	}
	for _, item := range orphans {
		if _, err := deleteAuthority(ctx, connection, item.jar, item.authority); err != nil {
			return result, err
		}
		result.OrphanAuthorities++
	}
	empty, err := deleteEmptyAuthorities(ctx, connection, nil)
	if err != nil {
		return result, err
	}
	result.EmptyAuthorities = empty
	return result, nil
}

func deleteJar(ctx context.Context, connection *sql.Conn, jar []byte) error {
	for _, statement := range []string{
		"DELETE FROM " + entriesTable + " WHERE jar_id = ?",
		"DELETE FROM " + authoritiesTable + " WHERE jar_id = ?",
		"DELETE FROM " + handlesTable + " WHERE jar_id = ?",
	} {
		if _, err := connection.ExecContext(ctx, statement, jar); err != nil {
			return classifyDatabaseError("delete_expired_binding", err)
		}
	}
	return nil
}

func deleteAuthority(ctx context.Context, connection *sql.Conn, jar, authority []byte) (int, error) {
	if _, err := connection.ExecContext(ctx, "DELETE FROM "+entriesTable+" WHERE jar_id = ? AND authority = ?", jar, authority); err != nil {
		return 0, classifyDatabaseError("delete_authority_cookies", err)
	}
	result, err := connection.ExecContext(ctx, "DELETE FROM "+authoritiesTable+" WHERE jar_id = ? AND authority = ?", jar, authority)
	if err != nil {
		return 0, classifyDatabaseError("delete_authority", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDatabaseError("delete_authority", err)
	}
	return int(count), nil
}

func deleteEmptyAuthorities(ctx context.Context, connection *sql.Conn, jar []byte) (int, error) {
	statement := "DELETE FROM " + authoritiesTable + " AS authorities WHERE NOT EXISTS (SELECT 1 FROM " + entriesTable + " AS entries WHERE entries.jar_id = authorities.jar_id AND entries.authority = authorities.authority)"
	arguments := []any(nil)
	if jar != nil {
		statement += " AND authorities.jar_id = ?"
		arguments = append(arguments, jar)
	}
	result, err := connection.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyDatabaseError("delete_empty_authorities", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDatabaseError("delete_empty_authorities", err)
	}
	return int(count), nil
}

func containsBytes(values [][]byte, target []byte) bool {
	for _, value := range values {
		if bytes.Equal(value, target) {
			return true
		}
	}
	return false
}
