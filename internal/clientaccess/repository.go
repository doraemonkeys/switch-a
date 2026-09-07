package clientaccess

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const policySingletonID = 1

type keyRecord struct {
	ID        string `gorm:"primaryKey"`
	Name      string
	Value     string    `gorm:"uniqueIndex;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"autoUpdateTime:false"`
}

func (keyRecord) TableName() string { return "client_api_keys" }

type policyRecord struct {
	ID   int  `gorm:"primaryKey"`
	Mode Mode `gorm:"not null"`
}

func (policyRecord) TableName() string { return "client_api_key_policy" }

type Repository struct{ db *gorm.DB }

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&keyRecord{}, &policyRecord{})
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot := Snapshot{Mode: ModePermissive, Keys: []Key{}}
	// Mode and membership are one admission decision, so separate autocommit
	// reads could incorrectly pair a new restriction with an old key set.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var policy policyRecord
		err := tx.First(&policy, policySingletonID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			snapshot.Mode = policy.Mode
		}
		var records []keyRecord
		if err := tx.Order("id").Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			snapshot.Keys = append(snapshot.Keys, record.key())
		}
		return ValidateSnapshot(snapshot)
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("read client API key policy: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) CreateKey(ctx context.Context, key Key) error {
	if err := validateRecord(key); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&keyRecord{}).Where("id = ? OR value = ?", key.ID, key.Key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrDuplicate
		}
		record := recordFromKey(key)
		return tx.Create(&record).Error
	})
}

func (r *Repository) RenameKey(ctx context.Context, id, name string, updatedAt time.Time) (Key, error) {
	if err := validateName(name); err != nil {
		return Key{}, err
	}
	var record keyRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&record, "id = ?", id).Error; err != nil {
			return recordError(err)
		}
		record.Name = name
		record.UpdatedAt = updatedAt
		return tx.Model(&record).Select("name", "updated_at").Updates(&record).Error
	})
	if err != nil {
		return Key{}, err
	}
	return record.key(), nil
}

func (r *Repository) DeleteKey(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&keyRecord{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) SetMode(ctx context.Context, mode Mode) error {
	if err := validateMode(mode); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(&policyRecord{ID: policySingletonID, Mode: mode}).Error
}

func (r *Repository) Replace(ctx context.Context, snapshot Snapshot) error {
	if err := ValidateSnapshot(snapshot); err != nil {
		return err
	}
	// When constructed with an import transaction, this savepoint remains
	// subordinate to the enclosing import: later failures roll back access too.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&keyRecord{}).Error; err != nil {
			return err
		}
		if len(snapshot.Keys) > 0 {
			records := make([]keyRecord, len(snapshot.Keys))
			for i, key := range snapshot.Keys {
				records[i] = recordFromKey(key)
			}
			if err := tx.Create(&records).Error; err != nil {
				return err
			}
		}
		return NewRepository(tx).SetMode(ctx, snapshot.Mode)
	})
}

func recordFromKey(key Key) keyRecord {
	return keyRecord{ID: key.ID, Name: key.Name, Value: key.Key, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt}
}

func (record keyRecord) key() Key {
	return Key{ID: record.ID, Name: record.Name, Key: record.Value, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func recordError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
