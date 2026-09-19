package continuation

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Binding records the current serving route, never the issuer of opaque state.
// Client IDs survive replacement API keys and keep unrelated clients separate.
type Binding struct {
	ClientID      string    `gorm:"primaryKey" json:"client_id"`
	Kind          string    `gorm:"primaryKey" json:"kind"`
	Digest        string    `gorm:"primaryKey" json:"digest"`
	ProviderID    string    `json:"provider_id"`
	ProtocolScope string    `json:"protocol_scope"`
	Outbound      Boundary  `json:"outbound"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Binding) TableName() string { return "codex_conversation_routes" }

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Lookup(ctx context.Context, key Binding) (Binding, bool, error) {
	var binding Binding
	err := r.db.WithContext(ctx).Where("client_id = ? AND kind = ? AND digest = ?", key.ClientID, key.Kind, key.Digest).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Binding{}, false, nil
	}
	return binding, err == nil, err
}

func (r *Repository) Save(ctx context.Context, bindings []Binding) error {
	if len(bindings) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}, {Name: "kind"}, {Name: "digest"}},
		DoUpdates: clause.AssignmentColumns([]string{"provider_id", "protocol_scope", "outbound", "updated_at"}),
	}).Create(&bindings).Error
}
