package store

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
)

func (s *SQLiteStore) ClientAPIKeyRepository() *clientaccess.Repository {
	return clientaccess.NewRepository(s.db)
}

func (s *SQLiteStore) ClientAPIKeySnapshot(ctx context.Context) (clientaccess.Snapshot, error) {
	return s.ClientAPIKeyRepository().Snapshot(ctx)
}
