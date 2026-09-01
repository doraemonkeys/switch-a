package credentialsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type uuidGenerator struct{}

func (uuidGenerator) NewID() string { return uuid.NewString() }

// Repository is a narrow SQLite/GORM repository for the CredentialSession
// aggregate. A transaction-scoped GORM handle can be supplied to compose route
// and session mutations atomically with provider writes.
type Repository struct {
	db    *gorm.DB
	clock Clock
	ids   IDGenerator
}

func NewRepository(db *gorm.DB, clock Clock, ids IDGenerator) (*Repository, error) {
	if db == nil || clock == nil {
		return nil, fmt.Errorf("credential session repository requires database and clock")
	}
	if ids == nil {
		ids = uuidGenerator{}
	}
	return &Repository{db: db, clock: clock, ids: ids}, nil
}

func (r *Repository) WithDB(db *gorm.DB) (*Repository, error) {
	if r == nil {
		return nil, fmt.Errorf("credential session repository is nil")
	}
	return NewRepository(db, r.clock, r.ids)
}

func (r *Repository) Create(ctx context.Context, session *Session) (*Session, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: session is nil", ErrInvalidSession)
	}
	candidate := session.Clone()
	if strings.TrimSpace(candidate.ID) == "" {
		candidate.ID = r.ids.NewID()
	}
	candidate.Name = strings.TrimSpace(candidate.Name)
	if candidate.Name == "" {
		candidate.Name = DefaultName(candidate.Kind, candidate.ID)
	}
	if candidate.Version < 1 {
		candidate.Version = 1
	}
	candidate.AuthState = NormalizeAuthState(candidate.Kind, candidate.AuthState)
	now := r.clock.Now().UTC()
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	if candidate.UpdatedAt.IsZero() {
		candidate.UpdatedAt = now
	}
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Create(candidate).Error; err != nil {
		return nil, fmt.Errorf("create credential session %q: %w", candidate.ID, err)
	}
	return candidate.Clone(), nil
}

func (r *Repository) Get(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	err := r.db.WithContext(ctx).First(&session, "id = ?", strings.TrimSpace(sessionID)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get credential session %q: %w", sessionID, err)
	}
	session.AuthState = NormalizeAuthState(session.Kind, session.AuthState)
	return session.Clone(), nil
}

func (r *Repository) List(ctx context.Context) ([]Session, error) {
	var sessions []Session
	if err := r.db.WithContext(ctx).Order("name COLLATE NOCASE ASC, id ASC").Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("list credential sessions: %w", err)
	}
	for index := range sessions {
		sessions[index].AuthState = NormalizeAuthState(sessions[index].Kind, sessions[index].AuthState)
	}
	return sessions, nil
}

// RouteReference is the operator-facing identity of one route that consumes a
// credential. Provider names are resolved at read time so renaming a provider
// never duplicates or stales credential metadata.
type RouteReference struct {
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	APIType      string `json:"api_type"`
}

func (r *Repository) ListRouteReferences(ctx context.Context, sessionID string) ([]RouteReference, error) {
	var references []RouteReference
	if err := r.db.WithContext(ctx).
		Table("route_target_credentials AS bindings").
		Select("bindings.route_target_id AS provider_id, providers.name AS provider_name, bindings.api_type").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("bindings.session_id = ?", strings.TrimSpace(sessionID)).
		Order("providers.name COLLATE NOCASE ASC, bindings.api_type ASC, bindings.route_target_id ASC").
		Scan(&references).Error; err != nil {
		return nil, fmt.Errorf("list route references for credential session %q: %w", sessionID, err)
	}
	return references, nil
}

func (r *Repository) Bind(ctx context.Context, binding RouteBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	now := r.clock.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Parent validation and the write share one serialization boundary. Foreign
		// keys remain the final guard if a concurrent delete commits first.
		if err := validateBindingTargets(ctx, tx, binding); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "route_target_id"}, {Name: "api_type"}},
			DoUpdates: clause.AssignmentColumns([]string{"session_id", "updated_at"}),
		}).Create(&binding).Error; err != nil {
			return fmt.Errorf("bind route target %q API type %q: %w", binding.RouteTargetID, binding.APIType, err)
		}
		return nil
	})
}

func (r *Repository) ReplaceRouteBindings(ctx context.Context, routeTargetID string, bindings []RouteBinding) error {
	routeTargetID = strings.TrimSpace(routeTargetID)
	if routeTargetID == "" {
		return ErrInvalidRouteBinding
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo, err := r.WithDB(tx)
		if err != nil {
			return err
		}
		if err := tx.Where("route_target_id = ?", routeTargetID).Delete(&RouteBinding{}).Error; err != nil {
			return fmt.Errorf("delete route credential bindings for %q: %w", routeTargetID, err)
		}
		seen := make(map[string]struct{}, len(bindings))
		for _, binding := range bindings {
			binding.RouteTargetID = routeTargetID
			if _, duplicate := seen[binding.APIType]; duplicate {
				return fmt.Errorf("%w: duplicate API type %q", ErrInvalidRouteBinding, binding.APIType)
			}
			seen[binding.APIType] = struct{}{}
			if err := txRepo.Bind(ctx, binding); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) Resolve(ctx context.Context, routeTargetID, apiType string) (RouteSnapshot, error) {
	var row struct {
		RouteTargetID string
		APIType       string
		VendorScope   string
		Session       Session `gorm:"embedded"`
	}
	err := r.db.WithContext(ctx).
		Table("route_target_credentials AS bindings").
		Select("bindings.route_target_id, bindings.api_type, providers.vendor AS vendor_scope, sessions.*").
		Joins("JOIN credential_sessions AS sessions ON sessions.id = bindings.session_id").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("bindings.route_target_id = ? AND bindings.api_type = ?", strings.TrimSpace(routeTargetID), strings.TrimSpace(apiType)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RouteSnapshot{}, ErrNotFound
	}
	if err != nil {
		return RouteSnapshot{}, fmt.Errorf("resolve route credential for %q/%q: %w", routeTargetID, apiType, err)
	}
	row.Session.AuthState = NormalizeAuthState(row.Session.Kind, row.Session.AuthState)
	snapshot, err := row.Session.Snapshot()
	if err != nil {
		return RouteSnapshot{}, fmt.Errorf("resolve route credential for %q/%q: %w", routeTargetID, apiType, err)
	}
	return RouteSnapshot{RouteTargetID: row.RouteTargetID, APIType: row.APIType, VendorScope: row.VendorScope, Credential: snapshot}, nil
}

func (r *Repository) ListRouteSnapshots(ctx context.Context, routeTargetIDs []string) (map[string][]RouteSnapshot, error) {
	result := make(map[string][]RouteSnapshot, len(routeTargetIDs))
	if len(routeTargetIDs) == 0 {
		return result, nil
	}
	type joined struct {
		RouteTargetID string
		APIType       string
		VendorScope   string
		Session       Session `gorm:"embedded"`
	}
	var rows []joined
	if err := r.db.WithContext(ctx).
		Table("route_target_credentials AS bindings").
		Select("bindings.route_target_id, bindings.api_type, providers.vendor AS vendor_scope, sessions.*").
		Joins("JOIN credential_sessions AS sessions ON sessions.id = bindings.session_id").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("bindings.route_target_id IN ?", routeTargetIDs).
		Order("bindings.route_target_id ASC, bindings.api_type ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list route credential snapshots: %w", err)
	}
	for index := range rows {
		rows[index].Session.AuthState = NormalizeAuthState(rows[index].Session.Kind, rows[index].Session.AuthState)
		snapshot, err := rows[index].Session.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("build route credential snapshot for %q/%q: %w", rows[index].RouteTargetID, rows[index].APIType, err)
		}
		result[rows[index].RouteTargetID] = append(result[rows[index].RouteTargetID], RouteSnapshot{
			RouteTargetID: rows[index].RouteTargetID,
			APIType:       rows[index].APIType,
			VendorScope:   rows[index].VendorScope,
			Credential:    snapshot,
		})
	}
	return result, nil
}

func (r *Repository) ListRouteTargetIDs(ctx context.Context, sessionID string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Model(&RouteBinding{}).
		Distinct("route_target_id").
		Where("session_id = ?", strings.TrimSpace(sessionID)).
		Order("route_target_id ASC").
		Pluck("route_target_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list route targets for credential session %q: %w", sessionID, err)
	}
	return ids, nil
}

func (r *Repository) ListEnabledRouteTargetIDs(ctx context.Context, sessionID string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Table("route_target_credentials AS bindings").
		Distinct("bindings.route_target_id").
		Joins("JOIN providers ON providers.id = bindings.route_target_id").
		Where("bindings.session_id = ? AND providers.enabled = ?", strings.TrimSpace(sessionID), true).
		Order("bindings.route_target_id ASC").
		Pluck("bindings.route_target_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list enabled route targets for credential session %q: %w", sessionID, err)
	}
	return ids, nil
}

func (r *Repository) DeleteRouteBindings(ctx context.Context, routeTargetID string) error {
	if err := r.db.WithContext(ctx).Where("route_target_id = ?", strings.TrimSpace(routeTargetID)).Delete(&RouteBinding{}).Error; err != nil {
		return fmt.Errorf("delete route credential bindings for %q: %w", routeTargetID, err)
	}
	return nil
}

func (r *Repository) DeleteIfUnreferenced(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&RouteBinding{}).Where("session_id = ?", strings.TrimSpace(sessionID)).Count(&count).Error; err != nil {
			return fmt.Errorf("count credential session references %q: %w", sessionID, err)
		}
		if count != 0 {
			return ErrSessionReferenced
		}
		result := tx.Delete(&Session{}, "id = ?", strings.TrimSpace(sessionID))
		if result.Error != nil {
			return fmt.Errorf("delete credential session %q: %w", sessionID, result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (r *Repository) RenameCAS(ctx context.Context, sessionID string, expectedVersion int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if expectedVersion < 1 || name == "" || len([]rune(name)) > MaxNameLength {
		return 0, fmt.Errorf("%w: name is required and must not exceed %d characters", ErrInvalidSession, MaxNameLength)
	}
	nextVersion := expectedVersion + 1
	result := r.db.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND version = ?", strings.TrimSpace(sessionID), expectedVersion).
		Updates(map[string]any{
			"name":       name,
			"version":    nextVersion,
			"updated_at": r.clock.Now().UTC(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("rename credential session %q: %w", sessionID, result.Error)
	}
	if result.RowsAffected == 0 {
		if _, err := r.Get(ctx, sessionID); errors.Is(err, ErrNotFound) {
			return 0, ErrNotFound
		}
		return 0, ErrVersionConflict
	}
	return nextVersion, nil
}

// UpdateCredentialCAS atomically rotates secret/subject/auth state and returns
// the new version. The caller must also hold the session mutation lease so
// process-local refresh work is singleflight before reaching this durable guard.
func (r *Repository) UpdateCredentialCAS(
	ctx context.Context,
	sessionID string,
	expectedVersion int64,
	secretData string,
	subject Subject,
	authState AuthState,
) (int64, error) {
	if strings.TrimSpace(secretData) == "" || expectedVersion < 1 {
		return 0, fmt.Errorf("%w: secret and positive expected version are required", ErrInvalidSession)
	}
	if err := subject.Validate(); err != nil {
		return 0, err
	}
	current, err := r.Get(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if err := validateSubjectForKind(current.Kind, subject); err != nil {
		return 0, err
	}
	authState = NormalizeAuthState(current.Kind, authState)
	if err := validateAuthStateForSubject(current.Kind, subject, authState); err != nil {
		return 0, err
	}
	authStateUpdates, err := authStateColumnUpdates(authState)
	if err != nil {
		return 0, fmt.Errorf("encode auth state for credential session %q: %w", sessionID, err)
	}
	nextVersion := expectedVersion + 1
	updates := map[string]any{
		"secret_data":         secretData,
		"version":             nextVersion,
		"subject_kind":        subject.Kind,
		"subject_value":       append([]byte(nil), subject.Value...),
		"subject_key_version": subject.KeyVersion,
		"updated_at":          r.clock.Now().UTC(),
	}
	for column, value := range authStateUpdates {
		updates[column] = value
	}
	result := r.db.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND version = ?", strings.TrimSpace(sessionID), expectedVersion).
		Updates(updates)
	if result.Error != nil {
		return 0, fmt.Errorf("update credential session %q: %w", sessionID, result.Error)
	}
	if result.RowsAffected == 0 {
		if _, err := r.Get(ctx, sessionID); errors.Is(err, ErrNotFound) {
			return 0, ErrNotFound
		}
		return 0, ErrVersionConflict
	}
	return nextVersion, nil
}

func (r *Repository) UpdateAuthStateCAS(ctx context.Context, sessionID string, expectedVersion int64, authState AuthState) (int64, error) {
	session, err := r.Get(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if expectedVersion < 1 {
		return 0, fmt.Errorf("%w: positive expected version is required", ErrInvalidSession)
	}
	authState = NormalizeAuthState(session.Kind, authState)
	if err := validateAuthStateForSubject(session.Kind, session.Subject(), authState); err != nil {
		return 0, err
	}
	updates, err := authStateColumnUpdates(authState)
	if err != nil {
		return 0, fmt.Errorf("encode auth state for credential session %q: %w", sessionID, err)
	}
	nextVersion := expectedVersion + 1
	updates["version"] = nextVersion
	updates["updated_at"] = r.clock.Now().UTC()
	result := r.db.WithContext(ctx).Model(&Session{}).Where("id = ? AND version = ?", session.ID, expectedVersion).Updates(updates)
	if result.Error != nil {
		return 0, fmt.Errorf("update auth state for credential session %q: %w", sessionID, result.Error)
	}
	if result.RowsAffected == 0 {
		return 0, ErrVersionConflict
	}
	return nextVersion, nil
}

func authStateColumnUpdates(authState AuthState) (map[string]any, error) {
	usageSnapshot, err := encodeUsageSnapshot(authState.UsageSnapshot)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"auth_status":                  authState.Status,
		"auth_status_reason":           authState.StatusReason,
		"auth_last_error":              authState.LastError,
		"auth_last_transition_at":      authState.LastTransitionAt,
		"auth_email":                   authState.Email,
		"auth_account_id":              authState.AccountID,
		"auth_plan_type":               authState.PlanType,
		"auth_expires_at":              authState.ExpiresAt,
		"auth_last_refresh_at":         authState.LastRefreshAt,
		"auth_usage_snapshot":          usageSnapshot,
		"auth_refresh_fail_count":      authState.RefreshFailCount,
		"auth_last_refresh_failure_at": authState.LastRefreshFailureAt,
	}, nil
}

func encodeUsageSnapshot(snapshot *UsageSnapshot) (any, error) {
	if snapshot == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode usage snapshot: %w", err)
	}
	// Map-based GORM updates bypass the model field's serializer, so the
	// repository must hand database/sql an already encoded scalar value.
	return string(encoded), nil
}

func (r *Repository) ResolvePendingSubject(ctx context.Context, sessionID string, subject Subject) error {
	if subject.Kind == SubjectPending {
		return fmt.Errorf("%w: resolved subject cannot be pending", ErrInvalidSession)
	}
	if err := subject.Validate(); err != nil {
		return err
	}
	session, err := r.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := validateSubjectForKind(session.Kind, subject); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND subject_kind = ?", strings.TrimSpace(sessionID), SubjectPending).
		Updates(map[string]any{
			"subject_kind":        subject.Kind,
			"subject_value":       append([]byte(nil), subject.Value...),
			"subject_key_version": subject.KeyVersion,
			"updated_at":          r.clock.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("resolve credential subject for session %q: %w", sessionID, result.Error)
	}
	if result.RowsAffected == 0 {
		session, err := r.Get(ctx, sessionID)
		if err != nil {
			return err
		}
		if session.Subject().Kind == subject.Kind && string(session.Subject().Value) == string(subject.Value) && session.SubjectKeyVersion == subject.KeyVersion {
			return nil
		}
		return fmt.Errorf("credential session %q already has a different subject", sessionID)
	}
	return nil
}

func validateBindingTargets(ctx context.Context, db *gorm.DB, binding RouteBinding) error {
	var session Session
	if err := db.WithContext(ctx).Select("id").First(&session, "id = ?", binding.SessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("validate credential session %q: %w", binding.SessionID, err)
	}
	var route struct{ ProviderID string }
	if err := db.WithContext(ctx).Table("provider_api_types AS api_types").
		Select("api_types.provider_id").
		Where("api_types.provider_id = ? AND api_types.api_type = ?", binding.RouteTargetID, binding.APIType).
		Take(&route).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidRouteBinding
		}
		return fmt.Errorf("validate route target %q API type %q: %w", binding.RouteTargetID, binding.APIType, err)
	}
	return nil
}
