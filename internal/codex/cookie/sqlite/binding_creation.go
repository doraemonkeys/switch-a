package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func (r *Repository) CreateJar(ctx context.Context, record providercookie.BindingRecord, authority codexidentity.CookieAuthority, mutations []providercookie.Mutation, policy providercookie.Policy) (providercookie.CreatedJar, error) {
	if ctx == nil {
		return providercookie.CreatedJar{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if err := validateBinding(record); err != nil {
		return providercookie.CreatedJar{}, err
	}
	scope, err := providercookie.NewCookieScope(record.JarID, authority)
	if err != nil {
		return providercookie.CreatedJar{}, err
	}
	overlay, err := providercookie.NewOverlay(scope, policy)
	if err != nil {
		return providercookie.CreatedJar{}, err
	}
	if err := overlay.ApplyBatch(scope, mutations); err != nil {
		return providercookie.CreatedJar{}, err
	}
	changes, err := overlay.Changes(scope)
	if err != nil {
		return providercookie.CreatedJar{}, err
	}
	live := false
	for _, change := range changes {
		if cookie, ok := change.Cookie(); ok && !cookie.Expired(record.CreatedAt) {
			live = true
			break
		}
	}
	if !live {
		return providercookie.CreatedJar{}, &providercookie.StateError{Reason: "a new jar requires live cookies"}
	}
	encoded, err := encodedAuthority(scope)
	if err != nil {
		return providercookie.CreatedJar{}, err
	}

	var release func()
	var merged providercookie.MergeResult
	err = withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		cleaned, err := r.cleanupStale(ctx, connection, record.CreatedAt, policy.OrphanAuthorityGrace)
		if err != nil {
			return err
		}
		if err := insertBinding(ctx, connection, record); err != nil {
			return err
		}
		merged, err = r.mergeTransaction(ctx, connection, scope, encoded, changes, record.CreatedAt, policy)
		merged.ReclaimedBindings += cleaned.ExpiredBindings
		if err == nil {
			release = r.activity.acquire(record.JarID)
		}
		return err
	})
	if err != nil {
		if release != nil {
			release()
		}
		return providercookie.CreatedJar{}, err
	}
	return providercookie.CreatedJar{Merge: merged, Release: release}, nil
}

func insertBinding(ctx context.Context, connection *sql.Conn, record providercookie.BindingRecord) error {
	clientDigest := record.ClientScope.Digest()
	_, err := connection.ExecContext(ctx, "INSERT INTO "+handlesTable+` (
		handle_key_version, handle_digest, jar_id, client_scope_key_version, client_scope_digest,
		created_at_ms, last_access_at_ms, idle_expires_at_ms, absolute_expires_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.HandleDigest.Version, record.HandleDigest.Sum[:], record.JarID.Bytes(),
		record.ClientScope.KeyVersion(), clientDigest[:], toMillis(record.CreatedAt),
		toMillis(record.LastAccessAt), toMillis(record.IdleExpiresAt), toMillis(record.AbsoluteExpiresAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return providercookie.ErrIdentifierClash
		}
		return classifyDatabaseError("create_binding", err)
	}
	return nil
}
