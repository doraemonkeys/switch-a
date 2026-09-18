// Package healthstate persists health independently for each provider route.
package healthstate

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/model/providerroute"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const providerTable = "health_states"
const routeTable = "route_health_states"

type ConfigSource interface {
	GetConfig(context.Context, string) (string, error)
}

type Store struct {
	db        *gorm.DB
	config    ConfigSource
	apiType   string
	transport string
}

func New(db *gorm.DB, config ConfigSource) *Store { return &Store{db: db, config: config} }
func (s *Store) HealthScope(apiType, transport string) internal.HealthStateStore {
	return &Store{db: s.db, config: s.config, apiType: apiType, transport: providerroute.Normalize(transport)}
}
func (s *Store) GetConfig(ctx context.Context, key string) (string, error) {
	return s.config.GetConfig(ctx, key)
}
func (s *Store) table() string {
	if s.apiType != "" {
		return routeTable
	}
	return providerTable
}
func (s *Store) key(id string) map[string]any {
	key := map[string]any{"provider_id": id}
	if s.apiType != "" {
		key["api_type"] = s.apiType
		key["transport"] = s.transport
	}
	return key
}
func (s *Store) query(ctx context.Context, id string) *gorm.DB {
	return s.db.WithContext(ctx).Table(s.table()).Where(s.key(id))
}
func (s *Store) upsert(ctx context.Context, id string, initial, updates map[string]any) error {
	columns := []clause.Column{{Name: "provider_id"}}
	if s.apiType != "" {
		columns = append(columns, clause.Column{Name: "api_type"}, clause.Column{Name: "transport"})
	}
	values := s.key(id)
	values["available"] = true
	values["success_count"] = 0
	values["fail_count"] = 0
	values["last_error"] = ""
	values["disabled_reason"] = ""
	maps.Copy(values, initial)
	return s.db.WithContext(ctx).Table(s.table()).Clauses(clause.OnConflict{Columns: columns, DoUpdates: clause.Assignments(updates)}).Create(values).Error
}
func (s *Store) GetHealthState(ctx context.Context, id string) (*model.HealthState, error) {
	var state model.HealthState
	err := s.query(ctx, id).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.HealthState{ProviderID: id, Available: true}, nil
	}
	return &state, err
}
func (s *Store) UpdateHealthState(ctx context.Context, state *model.HealthState) error {
	values := map[string]any{"available": state.Available, "success_count": state.SuccessCount, "fail_count": state.FailCount, "last_success": state.LastSuccess, "last_failure": state.LastFailure, "last_error": state.LastError, "disabled_until": state.DisabledUntil, "disabled_reason": state.DisabledReason}
	return s.upsert(ctx, state.ProviderID, values, values)
}
func (s *Store) IncrementSuccessCount(ctx context.Context, id string, now time.Time) (*model.HealthState, error) {
	updates := map[string]any{"success_count": gorm.Expr("success_count + 1"), "last_success": now, "available": gorm.Expr("CASE WHEN disabled_reason LIKE 'manual:%' OR (disabled_reason LIKE 'auto:%' AND disabled_until > ?) THEN available ELSE true END", now)}
	err := s.upsert(ctx, id, map[string]any{"success_count": 1, "last_success": now}, updates)
	if err != nil {
		return nil, err
	}
	return s.GetHealthState(ctx, id)
}
func (s *Store) IncrementFailCount(ctx context.Context, id string, now time.Time, lastError string) (*model.HealthState, error) {
	values := map[string]any{"fail_count": 1, "last_failure": now, "last_error": lastError}
	updates := map[string]any{"fail_count": gorm.Expr("fail_count + 1"), "last_failure": now, "last_error": lastError}
	if err := s.upsert(ctx, id, values, updates); err != nil {
		return nil, err
	}
	return s.GetHealthState(ctx, id)
}
func (s *Store) AutoDisableUntil(ctx context.Context, id string, until time.Time, reason string) error {
	initial := map[string]any{"available": false, "disabled_until": until, "disabled_reason": reason}
	updates := map[string]any{
		"available":       gorm.Expr("CASE WHEN disabled_reason LIKE 'manual:%' THEN available ELSE false END"),
		"disabled_until":  gorm.Expr("CASE WHEN disabled_reason LIKE 'manual:%' OR disabled_until >= ? THEN disabled_until ELSE ? END", until, until),
		"disabled_reason": gorm.Expr("CASE WHEN disabled_reason LIKE 'manual:%' OR disabled_until >= ? THEN disabled_reason ELSE ? END", until, reason),
	}
	return s.upsert(ctx, id, initial, updates)
}
func (s *Store) AtomicRecoverIfExpired(ctx context.Context, id string, now time.Time) (bool, error) {
	result := s.query(ctx, id).Where("available = false AND disabled_until IS NOT NULL AND disabled_until < ? AND disabled_reason NOT LIKE 'manual:%'", now).Updates(map[string]any{"available": true, "disabled_until": nil, "disabled_reason": ""})
	return result.RowsAffected > 0, result.Error
}
func Migrate(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE IF NOT EXISTS route_health_states (
 provider_id TEXT NOT NULL, api_type TEXT NOT NULL, transport TEXT NOT NULL,
 available NUMERIC NOT NULL DEFAULT 1, success_count INTEGER NOT NULL DEFAULT 0, fail_count INTEGER NOT NULL DEFAULT 0,
 last_success DATETIME, last_failure DATETIME, last_error TEXT NOT NULL DEFAULT '', disabled_until DATETIME, disabled_reason TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(provider_id, api_type, transport), FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE CASCADE)`).Error
}

// ResetRouteAvailability clears persisted circuit state even for routes that have
// not been used since restart. Provider-wide manual enable must restore all routes.
func (s *Store) ResetRouteAvailability(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Table(routeTable).Where("provider_id = ?", id).
		Updates(map[string]any{"available": true, "disabled_until": nil, "disabled_reason": ""}).Error
}
