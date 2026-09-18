package clientdisguise

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// ClientRequestObservation describes ingress activity independently of reference
// learning: a client must be recognizable before it can be chosen as a reference.
// This deployment-local evidence is deliberately not part of portable profiles.
type ClientRequestObservation struct {
	ClientID      string    `json:"-" gorm:"primaryKey"`
	ObservedAt    time.Time `json:"observed_at"`
	Tuple         Tuple     `json:"tuple" gorm:"embedded;embeddedPrefix:tuple_"`
	ClientVersion string    `json:"client_version"`
	UserAgent     string    `json:"user_agent"`
	Originator    string    `json:"originator"`
}

func (ClientRequestObservation) TableName() string { return "codex_client_request_observations" }

func (r *Repository) ListClientRequests(ctx context.Context) ([]ClientRequestObservation, error) {
	requests := []ClientRequestObservation{}
	err := r.db.WithContext(ctx).Order("observed_at DESC, client_id").Find(&requests).Error
	return requests, err
}

func (r *Repository) recordClientRequest(ctx context.Context, request ClientRequestObservation) error {
	// Requests can reach persistence out of order. Keep the timestamp and its
	// identifying features from the same, newest ingress observation.
	request.ObservedAt = request.ObservedAt.UTC().Round(0)
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"observed_at", "tuple_client_type", "tuple_platform", "tuple_arch", "client_version", "user_agent", "originator"}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Gt{Column: clause.Column{Table: "excluded", Name: "observed_at"}, Value: clause.Column{Table: clause.CurrentTable, Name: "observed_at"}},
		}},
	}).Create(&request).Error
}
