package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
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

// BindClientJar atomically reuses the one jar owned by a client scope or
// creates it when absent. Replacing the opaque handle on reuse lets clients
// that do support Cookies recover a usable alias without making Cookie
// retention a prerequisite for continuity.
func (r *Repository) BindClientJar(
	ctx context.Context,
	request providercookie.ClientJarBindingRequest,
) (providercookie.ClientJarBindingResult, error) {
	if ctx == nil {
		return providercookie.ClientJarBindingResult{}, &providercookie.ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if err := request.Policy.Validate(); err != nil {
		return providercookie.ClientJarBindingResult{}, err
	}
	if err := validateClientJarBindingRequest(request); err != nil {
		return providercookie.ClientJarBindingResult{}, err
	}

	var result providercookie.ClientJarBindingResult
	err := withImmediateTransaction(ctx, r.database, r.busyTimeout, func(connection *sql.Conn) error {
		if _, err := cleanupStale(ctx, connection, request.At, request.Policy.OrphanAuthorityGrace); err != nil {
			return err
		}
		records, err := findBindingsByClientScopes(ctx, connection, request.ClientScopeCandidates)
		if err != nil {
			return err
		}
		if len(records) > 1 {
			return corruptError("bind_client_jar", errors.New("multiple jars matched one client scope candidate set"))
		}
		if len(records) == 1 {
			rebound, err := rebindClientJar(ctx, connection, records[0], request)
			if err != nil {
				return err
			}
			result.Record = rebound
			return nil
		}

		var count int
		if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+handlesTable).Scan(&count); err != nil {
			return classifyDatabaseError("count_bindings", err)
		}
		if count >= request.Policy.MaxHandleBindingsGlobal {
			return &providercookie.LimitError{
				Limit:  providercookie.LimitHandleBindingsGlobal,
				Max:    request.Policy.MaxHandleBindingsGlobal,
				Actual: count + 1,
			}
		}
		if err := insertBinding(ctx, connection, request.ProposedBinding); err != nil {
			return err
		}
		result.Record = request.ProposedBinding
		result.Created = true
		return nil
	})
	return result, err
}

func validateClientJarBindingRequest(request providercookie.ClientJarBindingRequest) error {
	if request.At.IsZero() || len(request.ClientScopeCandidates) == 0 {
		return &providercookie.ConfigurationError{Field: "client_jar_binding", Reason: "time and client scope candidates are required"}
	}
	if _, err := request.CurrentClientScope.MarshalBinary(); err != nil {
		return &providercookie.ConfigurationError{Field: "current_client_scope", Reason: "must be initialized"}
	}
	seen := make(map[codexidentity.ClientScope]struct{}, len(request.ClientScopeCandidates))
	currentIncluded := false
	for _, scope := range request.ClientScopeCandidates {
		if _, err := scope.MarshalBinary(); err != nil {
			return &providercookie.ConfigurationError{Field: "client_scope_candidates", Reason: "contains an invalid scope"}
		}
		if _, exists := seen[scope]; exists {
			return &providercookie.ConfigurationError{Field: "client_scope_candidates", Reason: "contains a duplicate scope"}
		}
		seen[scope] = struct{}{}
		currentIncluded = currentIncluded || scope.Equal(request.CurrentClientScope)
	}
	if !currentIncluded {
		return &providercookie.ConfigurationError{Field: "client_scope_candidates", Reason: "must include the current scope"}
	}
	if err := validateBinding(request.ProposedBinding); err != nil {
		return err
	}
	if !request.ProposedBinding.ClientScope.Equal(request.CurrentClientScope) {
		return &providercookie.ConfigurationError{Field: "proposed_binding", Reason: "must be owned by the current client scope"}
	}
	if !request.ProposedBinding.CreatedAt.Equal(request.At) || !request.ProposedBinding.LastAccessAt.Equal(request.At) {
		return &providercookie.ConfigurationError{Field: "proposed_binding", Reason: "creation and access times must match the binding time"}
	}
	return nil
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

func rebindClientJar(
	ctx context.Context,
	connection *sql.Conn,
	record providercookie.BindingRecord,
	request providercookie.ClientJarBindingRequest,
) (providercookie.BindingRecord, error) {
	currentDigest := request.CurrentClientScope.Digest()
	var effectiveAccessMS, effectiveIdleMS int64
	err := connection.QueryRowContext(ctx,
		"UPDATE "+handlesTable+` SET
			handle_key_version = ?,
			handle_digest = ?,
			client_scope_key_version = ?,
			client_scope_digest = ?,
			last_access_at_ms = MAX(last_access_at_ms, ?),
			idle_expires_at_ms = MIN(absolute_expires_at_ms,
				MAX(idle_expires_at_ms, MAX(last_access_at_ms, ?) + ?))
		WHERE handle_key_version = ? AND handle_digest = ?
		RETURNING last_access_at_ms, idle_expires_at_ms`,
		request.ProposedBinding.HandleDigest.Version,
		request.ProposedBinding.HandleDigest.Sum[:],
		request.CurrentClientScope.KeyVersion(),
		currentDigest[:],
		toMillis(request.At),
		toMillis(request.At),
		request.Policy.HandleIdleTTL.Milliseconds(),
		record.HandleDigest.Version,
		record.HandleDigest.Sum[:],
	).Scan(&effectiveAccessMS, &effectiveIdleMS)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			return providercookie.BindingRecord{}, providercookie.ErrIdentifierClash
		}
		return providercookie.BindingRecord{}, classifyDatabaseError("rebind_client_jar", err)
	}
	record.HandleDigest = request.ProposedBinding.HandleDigest
	record.ClientScope = request.CurrentClientScope
	record.LastAccessAt = fromMillis(effectiveAccessMS)
	record.IdleExpiresAt = fromMillis(effectiveIdleMS)
	return record, nil
}

func findBindingsByClientScopes(
	ctx context.Context,
	connection *sql.Conn,
	scopes []codexidentity.ClientScope,
) ([]providercookie.BindingRecord, error) {
	predicates := make([]string, 0, len(scopes))
	arguments := make([]any, 0, len(scopes)*2)
	for _, scope := range scopes {
		digest := scope.Digest()
		predicates = append(predicates, "(client_scope_key_version = ? AND client_scope_digest = ?)")
		arguments = append(arguments, scope.KeyVersion(), digest[:])
	}
	return queryBindings(ctx, connection, strings.Join(predicates, " OR "), arguments)
}
