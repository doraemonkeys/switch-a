package store

import (
	"context"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm/clause"
)

// stickyEntryRecord stores only the dimensions needed to reconstruct a sticky
// key. The composite key prevents one client dimension from overwriting another
// while keeping lookups index-friendly for SQLite.
type stickyEntryRecord struct {
	IP         string    `gorm:"primaryKey;column:ip"`
	User       string    `gorm:"primaryKey;column:user"`
	APIType    string    `gorm:"primaryKey;column:api_type"`
	Model      string    `gorm:"primaryKey;column:model"`
	ProviderID string    `gorm:"not null;index;column:provider_id"`
	ExpiresAt  time.Time `gorm:"not null;index;column:expires_at"`
	UpdatedAt  time.Time `gorm:"not null;column:updated_at"`
}

func (stickyEntryRecord) TableName() string { return "sticky_entries" }

// LoadStickyEntries returns only live bindings. Filtering in SQL prevents an
// outage that lasts beyond the TTL from resurrecting stale affinity at startup.
func (s *SQLiteStore) LoadStickyEntries(ctx context.Context, now time.Time) ([]model.StickyEntry, error) {
	var records []stickyEntryRecord
	if err := s.db.WithContext(ctx).
		Where("expires_at > ?", now).
		Find(&records).Error; err != nil {
		return nil, err
	}

	entries := make([]model.StickyEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, model.StickyEntry{
			Key: model.StickyKey{
				IP:      record.IP,
				User:    record.User,
				APIType: record.APIType,
				Model:   record.Model,
			},
			ProviderID: record.ProviderID,
			ExpiresAt:  record.ExpiresAt,
		})
	}
	return entries, nil
}

// UpsertStickyEntry mirrors one memory mutation. It intentionally does not
// compare timestamps: the single process is authoritative and persistence is
// a recovery aid, not a cross-process coordination protocol.
func (s *SQLiteStore) UpsertStickyEntry(ctx context.Context, entry model.StickyEntry) error {
	record := stickyEntryRecord{
		IP:         entry.Key.IP,
		User:       entry.Key.User,
		APIType:    entry.Key.APIType,
		Model:      entry.Key.Model,
		ProviderID: entry.ProviderID,
		ExpiresAt:  entry.ExpiresAt,
		UpdatedAt:  s.clock.Now(),
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ip"},
			{Name: "user"},
			{Name: "api_type"},
			{Name: "model"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"provider_id", "expires_at", "updated_at"}),
	}).Create(&record).Error
}

// DeleteStickyEntry removes one binding from durable storage.
func (s *SQLiteStore) DeleteStickyEntry(ctx context.Context, key model.StickyKey) error {
	return s.db.WithContext(ctx).
		Where("ip = ? AND user = ? AND api_type = ? AND model = ?", key.IP, key.User, key.APIType, key.Model).
		Delete(&stickyEntryRecord{}).Error
}

// DeleteStickyEntriesByProvider eagerly invalidates all affinity for a provider.
func (s *SQLiteStore) DeleteStickyEntriesByProvider(ctx context.Context, providerID string) error {
	if providerID == "" {
		return nil
	}
	return s.db.WithContext(ctx).Where("provider_id = ?", providerID).Delete(&stickyEntryRecord{}).Error
}

// DeleteExpiredStickyEntries bounds durable state even when no requests arrive
// after an entry expires.
func (s *SQLiteStore) DeleteExpiredStickyEntries(ctx context.Context, now time.Time) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&stickyEntryRecord{}).Error
}
