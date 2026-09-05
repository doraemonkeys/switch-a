package store

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func (s *SQLiteStore) SetCodexStickyRestorer(restore func([]model.StickyEntry)) {
	s.codexStickyRestorer = restore
}
func codexStickyEntries(entries []model.StickyEntry) []model.StickyEntry {
	result := []model.StickyEntry{}
	for _, entry := range entries {
		if entry.Key.APIType == stickyCodexAPIType {
			result = append(result, entry)
		}
	}
	return result
}
func importCodexSticky(ctx context.Context, s *SQLiteStore, state *CodexState) error {
	if state == nil {
		return nil
	}
	for _, entry := range state.Sticky {
		if entry.Key.APIType != stickyCodexAPIType || entry.Key.ClientScope == "" {
			return fmt.Errorf("invalid Codex sticky entry")
		}
		var count int64
		if err := s.db.Model(&model.Provider{}).Where("id = ?", entry.ProviderID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("sticky entry references missing provider %s", entry.ProviderID)
		}
		var current stickyEntryRecord
		query := s.db.Where("ip = ? AND user = ? AND api_type = ? AND model = ? AND client_scope = ?", entry.Key.IP, entry.Key.User, entry.Key.APIType, entry.Key.Model, entry.Key.ClientScope)
		if err := query.Find(&current).Error; err != nil {
			return err
		}
		// A backup is recovery input; a live affinity winner remains authoritative.
		if current.ExpiresAt.After(s.clock.Now()) || !entry.ExpiresAt.After(s.clock.Now()) {
			continue
		}
		if err := s.UpsertStickyEntry(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func snapshotCommittedSticky(ctx context.Context, s *SQLiteStore, bundle *ConfigImportBundle) error {
	if bundle.restoredSticky == nil || bundle.CodexState == nil {
		return nil
	}
	entries, err := s.LoadStickyEntries(ctx, s.clock.Now())
	if err != nil {
		return err
	}
	selected := map[model.StickyKey]bool{}
	for _, entry := range bundle.CodexState.Sticky {
		selected[entry.Key] = true
	}
	for _, entry := range entries {
		if selected[entry.Key] {
			*bundle.restoredSticky = append(*bundle.restoredSticky, entry)
		}
	}
	return nil
}
