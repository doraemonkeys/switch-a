package sqlite

import (
	"context"
	"database/sql"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

type capacityCandidate struct {
	jar     []byte
	entries int
}

// Reclaim whole idle jars so a client never inherits another client's state.
// Prefer empty jars, then handles never returned, then least recently reused.
func (r *Repository) reclaimCapacity(ctx context.Context, connection *sql.Conn, current providercookie.JarID, policy providercookie.Policy) (int, error) {
	var bindings, entries int
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+handlesTable).Scan(&bindings); err != nil {
		return 0, classifyDatabaseError("count_bindings", err)
	}
	if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+entriesTable).Scan(&entries); err != nil {
		return 0, classifyDatabaseError("count_global_cookies", err)
	}
	if bindings <= policy.MaxHandleBindingsGlobal && entries <= policy.MaxCookieEntriesGlobal {
		return 0, nil
	}
	rows, err := connection.QueryContext(ctx, `SELECT h.jar_id, COUNT(e.cookie_name)
		FROM `+handlesTable+` h LEFT JOIN `+entriesTable+` e ON e.jar_id = h.jar_id
		WHERE h.jar_id != ? GROUP BY h.jar_id
		ORDER BY (COUNT(e.cookie_name) > 0), (h.last_returned_at_ms IS NOT NULL),
		h.last_access_at_ms, h.created_at_ms, hex(h.jar_id)`, current.Bytes())
	if err != nil {
		return 0, classifyDatabaseError("select_binding_evictions", err)
	}
	candidates := make([]capacityCandidate, 0)
	for rows.Next() {
		var candidate capacityCandidate
		if err := rows.Scan(&candidate.jar, &candidate.entries); err != nil {
			_ = rows.Close()
			return 0, classifyDatabaseError("select_binding_evictions", err)
		}
		if !r.activity.active(candidate.jar) {
			candidates = append(candidates, candidate)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, classifyDatabaseError("select_binding_evictions", err)
	}
	reclaimed := 0
	for _, candidate := range candidates {
		if bindings <= policy.MaxHandleBindingsGlobal && entries <= policy.MaxCookieEntriesGlobal {
			break
		}
		if err := deleteJar(ctx, connection, candidate.jar); err != nil {
			return 0, err
		}
		bindings--
		entries -= candidate.entries
		reclaimed++
	}
	if bindings > policy.MaxHandleBindingsGlobal {
		return 0, &providercookie.LimitError{Limit: providercookie.LimitHandleBindingsGlobal, Max: policy.MaxHandleBindingsGlobal, Actual: bindings}
	}
	if entries > policy.MaxCookieEntriesGlobal {
		return 0, &providercookie.LimitError{Limit: providercookie.LimitGlobalEntries, Max: policy.MaxCookieEntriesGlobal, Actual: entries}
	}
	return reclaimed, nil
}

func (r *Repository) deleteEmptyBindings(ctx context.Context, connection *sql.Conn) (int, error) {
	rows, err := connection.QueryContext(ctx, "SELECT jar_id FROM "+handlesTable+` h
		WHERE NOT EXISTS (SELECT 1 FROM `+entriesTable+` e WHERE e.jar_id = h.jar_id)`)
	if err != nil {
		return 0, classifyDatabaseError("find_empty_bindings", err)
	}
	jars := make([][]byte, 0)
	for rows.Next() {
		var jar []byte
		if err := rows.Scan(&jar); err != nil {
			_ = rows.Close()
			return 0, classifyDatabaseError("find_empty_bindings", err)
		}
		if !r.activity.active(jar) {
			jars = append(jars, jar)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, classifyDatabaseError("find_empty_bindings", err)
	}
	for _, jar := range jars {
		if err := deleteJar(ctx, connection, jar); err != nil {
			return 0, err
		}
	}
	return len(jars), nil
}
