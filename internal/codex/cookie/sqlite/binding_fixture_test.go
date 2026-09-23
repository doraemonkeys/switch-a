package sqlite

import (
	"context"
	"database/sql"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

// Empty bindings exist in pre-v4 databases. Tests seed them directly to cover
// migration and reclamation; production can only create jars with cookies.
func (r *Repository) seedBinding(ctx context.Context, record providercookie.BindingRecord, policy providercookie.Policy) error {
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
		if _, err := r.cleanupStale(ctx, connection, record.CreatedAt, policy.OrphanAuthorityGrace); err != nil {
			return err
		}
		return insertBinding(ctx, connection, record)
	})
}
