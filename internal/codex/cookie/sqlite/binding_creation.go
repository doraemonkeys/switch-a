package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

func (r *Repository) CreateBinding(ctx context.Context, record providercookie.BindingRecord, policy providercookie.Policy) error {
	if ctx == nil {
		return &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if err := validateBinding(record); err != nil {
		return err
	}
	return withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		if _, err := cleanupStale(ctx, connection, record.CreatedAt, policy.OrphanAuthorityGrace); err != nil {
			return err
		}
		var count int
		if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+handlesTable).Scan(&count); err != nil {
			return classifyDatabaseError("count_bindings", err)
		}
		if count >= policy.MaxHandleBindingsGlobal {
			return &providercookie.LimitError{
				Limit:  providercookie.LimitHandleBindingsGlobal,
				Max:    policy.MaxHandleBindingsGlobal,
				Actual: count + 1,
			}
		}
		return insertBinding(ctx, connection, record)
	})
}

func insertBinding(ctx context.Context, connection *sql.Conn, record providercookie.BindingRecord) error {
	clientDigest := record.ClientScope.Digest()
	_, err := connection.ExecContext(ctx, "INSERT INTO "+handlesTable+` (
		handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest,
		created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.HandleDigest.Version,
		record.HandleDigest.Sum[:],
		record.JarID.Bytes(),
		record.ClientScope.KeyVersion(),
		clientDigest[:],
		toMillis(record.CreatedAt),
		toMillis(record.LastAccessAt),
		toMillis(record.IdleExpiresAt),
		toMillis(record.AbsoluteExpiresAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return providercookie.ErrIdentifierClash
		}
		return classifyDatabaseError("create_binding", err)
	}
	return nil
}
