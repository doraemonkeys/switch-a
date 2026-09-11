package sqlite

import (
	"context"
	"fmt"
	"sort"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func (s *Snapshot) ReadClientAPIKeys(ctx context.Context) ([]tokenanalytics.ClientAPIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errSnapshotClosed
	}

	keys, err := s.readObservedClientAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	// Resolve current names by credential value: deleting and re-registering a
	// key must not split usage, and registering an observed key should name its
	// existing history. Raw values never leave this repository boundary.
	rows, err := s.tx.QueryContext(ctx, "SELECT name, value FROM client_api_keys")
	if err != nil {
		return nil, fmt.Errorf("read registered client API keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan registered client API key: %w", err)
		}
		identity := clientaccess.IdentifyKey([]byte(value))
		keys[identity.Fingerprint] = tokenanalytics.ClientAPIKey{
			Fingerprint: identity.Fingerprint, Name: name, MaskedKey: identity.MaskedKey,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read registered client API keys: %w", err)
	}
	result := make([]tokenanalytics.ClientAPIKey, 0, len(keys))
	for _, key := range keys {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name > "" && (result[j].Name == "" || result[i].Name < result[j].Name)
		}
		return result[i].Fingerprint < result[j].Fingerprint
	})
	return result, nil
}

func (s *Snapshot) readObservedClientAPIKeys(ctx context.Context) (map[string]tokenanalytics.ClientAPIKey, error) {
	rows, err := s.tx.QueryContext(ctx, `SELECT client_api_key_fingerprint, MAX(client_api_key_masked)
		FROM request_logs INDEXED BY `+clientAPIKeyCreatedAtIndex+`
		WHERE client_api_key_fingerprint != '' GROUP BY client_api_key_fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("read observed client API keys: %w", err)
	}
	defer rows.Close()
	keys := make(map[string]tokenanalytics.ClientAPIKey)
	for rows.Next() {
		var key tokenanalytics.ClientAPIKey
		if err := rows.Scan(&key.Fingerprint, &key.MaskedKey); err != nil {
			return nil, fmt.Errorf("scan observed client API key: %w", err)
		}
		keys[key.Fingerprint] = key
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read observed client API keys: %w", err)
	}
	return keys, nil
}
