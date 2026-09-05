package store

import (
	"context"
	"errors"
	"fmt"
)

var errConfigImportPreview = errors.New("rollback successful config import preview")

func (s *SQLiteStore) PreviewConfigImport(ctx context.Context, bundle *ConfigImportBundle) error {
	if bundle == nil {
		return nil
	}
	preview := *bundle
	preview.preview = true
	err := s.ApplyConfigImport(ctx, &preview)
	if errors.Is(err, errConfigImportPreview) {
		return nil
	}
	return err
}
func (s *CachedStore) PreviewConfigImport(ctx context.Context, bundle *ConfigImportBundle) error {
	if source, ok := s.Store.(interface {
		PreviewConfigImport(context.Context, *ConfigImportBundle) error
	}); ok {
		return source.PreviewConfigImport(ctx, bundle)
	}
	return fmt.Errorf("config import preview unavailable")
}
