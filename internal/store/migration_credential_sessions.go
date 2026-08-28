package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const credentialSessionMigrationID = "m1-credential-sessions-v1"

// StaticCredentialSubjectSigner is deliberately consumer-owned. KR1 adapts its
// versioned HMAC signer to this capability without making credential persistence
// responsible for loading secret files or knowing keyring configuration.
type StaticCredentialSubjectSigner interface {
	Sign(purpose codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error)
}

type credentialSessionMigration struct {
	ID        string    `gorm:"column:id;primaryKey;type:text"`
	AppliedAt time.Time `gorm:"column:applied_at;not null"`
}

func (credentialSessionMigration) TableName() string { return "credential_session_migrations" }

type legacyCredentialProvider struct {
	ID             string
	Vendor         string
	APIKey         string
	CredentialType string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type legacyCredentialAPIType struct {
	ProviderID string
	APIType    string
	APIKey     string
}

type legacyCredentialRecord struct {
	ProviderID       string
	SecretData       string
	BindingAccountID *string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type legacyAuthStateRecord struct {
	ProviderID           string
	Status               credentialsession.AuthStatus
	StatusReason         string
	LastError            string
	LastTransitionAt     *time.Time
	Email                string
	AccountID            string
	PlanType             string
	ExpiresAt            *time.Time
	LastRefreshAt        *time.Time
	UsageSnapshot        string
	RefreshFailCount     int
	LastRefreshFailureAt *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// migrateCredentialSessions performs the only supported transition from
// provider-owned credentials to independently referenced sessions. Structural
// migration and backfill are one SQLite transaction. Static subjects remain
// pending until bootstrap has inspected every durable key family and selected a
// complete signer.
func migrateCredentialSessions(
	db *gorm.DB,
	clock internalClock,
) error {
	if db == nil || clock == nil {
		return fmt.Errorf("credential session migration requires database and clock")
	}
	ctx := context.Background()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := createCredentialSessionSchema(tx); err != nil {
			return err
		}
		migrated, err := credentialMigrationApplied(tx)
		if err != nil {
			return err
		}
		if !migrated {
			if err := migrateProviderOwnedCredentials(tx, clock); err != nil {
				return err
			}
			if err := tx.Create(&credentialSessionMigration{
				ID:        credentialSessionMigrationID,
				AppliedAt: clock.Now().UTC(),
			}).Error; err != nil {
				return fmt.Errorf("record credential session migration: %w", err)
			}
		}
		return validateCredentialSessionSchema(tx)
	}); err != nil {
		return err
	}
	return nil
}

// internalClock keeps this migration independent from the broad Store contract.
type internalClock interface {
	Now() time.Time
}

func createCredentialSessionSchema(tx *gorm.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS credential_sessions (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			secret_data TEXT NOT NULL,
			version INTEGER NOT NULL CHECK(version > 0),
			subject_kind TEXT NOT NULL,
			subject_value BLOB,
			subject_key_version TEXT NOT NULL DEFAULT '',
			auth_status TEXT NOT NULL DEFAULT 'not_connected',
			auth_status_reason TEXT NOT NULL DEFAULT '',
			auth_last_error TEXT NOT NULL DEFAULT '',
			auth_last_transition_at DATETIME,
			auth_email TEXT NOT NULL DEFAULT '',
			auth_account_id TEXT NOT NULL DEFAULT '',
			auth_plan_type TEXT NOT NULL DEFAULT '',
			auth_expires_at DATETIME,
			auth_last_refresh_at DATETIME,
			auth_usage_snapshot TEXT,
			auth_refresh_fail_count INTEGER NOT NULL DEFAULT 0,
			auth_last_refresh_failure_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_sessions_kind ON credential_sessions(kind)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_sessions_subject_kind ON credential_sessions(subject_kind)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_sessions_auth_status ON credential_sessions(auth_status)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_sessions_auth_account_id ON credential_sessions(auth_account_id)`,
		`CREATE TABLE IF NOT EXISTS route_target_credentials (
			route_target_id TEXT NOT NULL,
			api_type TEXT NOT NULL,
			session_id TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(route_target_id, api_type),
			FOREIGN KEY(route_target_id, api_type) REFERENCES provider_api_types(provider_id, api_type) ON DELETE CASCADE,
			FOREIGN KEY(session_id) REFERENCES credential_sessions(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_route_target_credentials_session_id ON route_target_credentials(session_id)`,
		`CREATE TABLE IF NOT EXISTS credential_session_migrations (
			id TEXT PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create credential session schema: %w", err)
		}
	}
	return nil
}

func credentialMigrationApplied(tx *gorm.DB) (bool, error) {
	var count int64
	if err := tx.Model(&credentialSessionMigration{}).Where("id = ?", credentialSessionMigrationID).Count(&count).Error; err != nil {
		return false, fmt.Errorf("read credential session migration marker: %w", err)
	}
	return count != 0, nil
}

func migrateProviderOwnedCredentials(tx *gorm.DB, clock internalClock) error {
	providersExist := tx.Migrator().HasTable("providers")
	apiTypesExist := tx.Migrator().HasTable("provider_api_types")
	if !providersExist && !apiTypesExist {
		return nil
	}
	if providersExist != apiTypesExist {
		return fmt.Errorf("credential session migration found incomplete provider schema")
	}
	hasProviderSecret := tx.Migrator().HasColumn("providers", "api_key")
	hasProviderKind := tx.Migrator().HasColumn("providers", "credential_type")
	hasAPITypeSecret := tx.Migrator().HasColumn("provider_api_types", "api_key")
	if !hasProviderSecret && !hasProviderKind && !hasAPITypeSecret {
		return nil
	}
	if !hasProviderSecret || !hasProviderKind || !hasAPITypeSecret {
		return fmt.Errorf("credential session migration found partially removed legacy credential columns")
	}

	var providers []legacyCredentialProvider
	if err := tx.Table("providers").Select("id, vendor, api_key, credential_type, created_at, updated_at").Order("id ASC").Scan(&providers).Error; err != nil {
		return fmt.Errorf("list legacy credential providers: %w", err)
	}
	var apiTypes []legacyCredentialAPIType
	if err := tx.Table("provider_api_types").Select("provider_id, api_type, api_key").Order("provider_id ASC, api_type ASC").Scan(&apiTypes).Error; err != nil {
		return fmt.Errorf("list legacy provider API types: %w", err)
	}
	credentials, err := loadLegacyCredentialRecords(tx)
	if err != nil {
		return err
	}
	authStates, err := loadLegacyAuthStateRecords(tx)
	if err != nil {
		return err
	}
	apiTypesByProvider := make(map[string][]legacyCredentialAPIType)
	for _, apiType := range apiTypes {
		apiTypesByProvider[apiType.ProviderID] = append(apiTypesByProvider[apiType.ProviderID], apiType)
	}

	repository, err := credentialsession.NewRepository(tx, clock, nil)
	if err != nil {
		return err
	}
	for _, provider := range providers {
		kind := credentialsession.Kind(strings.TrimSpace(provider.CredentialType))
		if kind == "" {
			kind = credentialsession.KindAPIKey
		}
		switch kind {
		case credentialsession.KindAPIKey:
			if err := backfillStaticProviderSessions(tx, repository, provider, apiTypesByProvider[provider.ID], clock); err != nil {
				return err
			}
		case credentialsession.KindChatGPT:
			if err := backfillLoginProviderSession(tx, repository, provider, apiTypesByProvider[provider.ID], credentials[provider.ID], authStates[provider.ID], clock); err != nil {
				return err
			}
		default:
			return fmt.Errorf("provider %q has unsupported legacy credential type %q", provider.ID, provider.CredentialType)
		}
	}

	if err := dropLegacyCredentialStorage(tx); err != nil {
		return err
	}
	return nil
}

func backfillStaticProviderSessions(
	tx *gorm.DB,
	repository *credentialsession.Repository,
	provider legacyCredentialProvider,
	apiTypes []legacyCredentialAPIType,
	clock internalClock,
) error {
	defaultSecret := strings.TrimSpace(provider.APIKey)
	defaultSessionID := ""
	if defaultSecret != "" {
		created, err := createMigratedStaticSession(repository, provider, defaultSecret)
		if err != nil {
			return fmt.Errorf("create default static credential session for provider %q: %w", provider.ID, err)
		}
		defaultSessionID = created.ID
	}
	for _, apiType := range apiTypes {
		overrideSecret := strings.TrimSpace(apiType.APIKey)
		if overrideSecret == "" && defaultSessionID == "" {
			return fmt.Errorf("provider %q API type %q has no credential to migrate", provider.ID, apiType.APIType)
		}
		sessionID := defaultSessionID
		if overrideSecret != "" {
			created, err := createMigratedStaticSession(repository, provider, overrideSecret)
			if err != nil {
				return fmt.Errorf("create static credential override for provider %q API type %q: %w", provider.ID, apiType.APIType, err)
			}
			sessionID = created.ID
		}
		if err := insertMigrationBinding(tx, provider.ID, apiType.APIType, sessionID, provider.CreatedAt, provider.UpdatedAt, clock.Now()); err != nil {
			return err
		}
	}
	return nil
}

func createMigratedStaticSession(
	repository *credentialsession.Repository,
	provider legacyCredentialProvider,
	secret string,
) (*credentialsession.Session, error) {
	session := &credentialsession.Session{
		ID:         uuid.NewString(),
		Kind:       credentialsession.KindAPIKey,
		SecretData: secret,
		Version:    1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive,
		},
		CreatedAt: provider.CreatedAt,
		UpdatedAt: provider.UpdatedAt,
	}
	if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
		return nil, err
	}
	return repository.Create(context.Background(), session)
}

func backfillLoginProviderSession(
	tx *gorm.DB,
	repository *credentialsession.Repository,
	provider legacyCredentialProvider,
	apiTypes []legacyCredentialAPIType,
	credential *legacyCredentialRecord,
	auth *legacyAuthStateRecord,
	clock internalClock,
) error {
	if credential == nil || strings.TrimSpace(credential.SecretData) == "" {
		if len(apiTypes) == 0 {
			return nil
		}
		return fmt.Errorf("login provider %q has no credential session to migrate", provider.ID)
	}
	authState, err := migrateLegacyAuthState(auth)
	if err != nil {
		return fmt.Errorf("migrate auth state for provider %q: %w", provider.ID, err)
	}
	bindingAccountID := ""
	if credential.BindingAccountID != nil {
		bindingAccountID = strings.TrimSpace(*credential.BindingAccountID)
	}
	diagnosticAccountID := strings.TrimSpace(authState.AccountID)
	if bindingAccountID != "" && diagnosticAccountID != "" && bindingAccountID != diagnosticAccountID {
		return fmt.Errorf("login provider %q binding account proof conflicts with diagnostic auth account", provider.ID)
	}
	var subject credentialsession.Subject
	if bindingAccountID == "" {
		if authState.Status != credentialsession.AuthStatusReauthRequired {
			return fmt.Errorf("login provider %q has no binding account proof outside reauthentication recovery", provider.ID)
		}
		subject = credentialsession.PendingSubject()
	} else {
		subject, err = credentialsession.AccountSubject(bindingAccountID)
		if err != nil {
			return fmt.Errorf("derive login subject for provider %q: %w", provider.ID, err)
		}
	}
	createdAt := credential.CreatedAt
	if createdAt.IsZero() {
		createdAt = provider.CreatedAt
	}
	updatedAt := credential.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = provider.UpdatedAt
	}
	session := &credentialsession.Session{
		ID:         uuid.NewString(),
		Kind:       credentialsession.KindChatGPT,
		SecretData: credential.SecretData,
		Version:    max(credential.Version, 1),
		AuthState:  authState,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
	if err := session.SetSubject(subject); err != nil {
		return err
	}
	created, err := repository.Create(context.Background(), session)
	if err != nil {
		return err
	}
	for _, apiType := range apiTypes {
		if err := insertMigrationBinding(tx, provider.ID, apiType.APIType, created.ID, provider.CreatedAt, provider.UpdatedAt, clock.Now()); err != nil {
			return err
		}
	}
	return nil
}

func insertMigrationBinding(tx *gorm.DB, routeTargetID, apiType, sessionID string, createdAt, updatedAt, fallback time.Time) error {
	if createdAt.IsZero() {
		createdAt = fallback.UTC()
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	binding := credentialsession.RouteBinding{
		RouteTargetID: routeTargetID,
		APIType:       apiType,
		SessionID:     sessionID,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return fmt.Errorf("backfill route credential binding %q/%q: %w", routeTargetID, apiType, err)
	}
	return nil
}

func staticSubject(secret string, signer StaticCredentialSubjectSigner) (credentialsession.Subject, error) {
	if signer == nil {
		return credentialsession.Subject{}, fmt.Errorf("static credential subject signer is required")
	}
	input, err := credentialsession.StaticSubjectInput(credentialsession.KindAPIKey, secret)
	if err != nil {
		return credentialsession.Subject{}, err
	}
	digest, err := signer.Sign(codexkeyring.HMACCredentialSubject, input)
	if err != nil {
		return credentialsession.Subject{}, err
	}
	return credentialsession.KeyedDigestSubject(digest.Version, digest.Sum[:])
}

func loadLegacyCredentialRecords(tx *gorm.DB) (map[string]*legacyCredentialRecord, error) {
	result := make(map[string]*legacyCredentialRecord)
	if !tx.Migrator().HasTable("provider_credentials") {
		return result, nil
	}
	var rows []legacyCredentialRecord
	if err := tx.Table("provider_credentials").Order("provider_id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list legacy provider credentials: %w", err)
	}
	for index := range rows {
		row := rows[index]
		result[row.ProviderID] = &row
	}
	return result, nil
}

func loadLegacyAuthStateRecords(tx *gorm.DB) (map[string]*legacyAuthStateRecord, error) {
	result := make(map[string]*legacyAuthStateRecord)
	if !tx.Migrator().HasTable("provider_auth_states") {
		return result, nil
	}
	var rows []legacyAuthStateRecord
	if err := tx.Table("provider_auth_states").Order("provider_id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list legacy provider auth states: %w", err)
	}
	for index := range rows {
		row := rows[index]
		result[row.ProviderID] = &row
	}
	return result, nil
}

func migrateLegacyAuthState(source *legacyAuthStateRecord) (credentialsession.AuthState, error) {
	if source == nil {
		return credentialsession.AuthState{Status: credentialsession.AuthStatusNotConnected}, nil
	}
	state := credentialsession.AuthState{
		Status:               source.Status,
		StatusReason:         source.StatusReason,
		LastError:            source.LastError,
		LastTransitionAt:     source.LastTransitionAt,
		Email:                source.Email,
		AccountID:            source.AccountID,
		PlanType:             source.PlanType,
		ExpiresAt:            source.ExpiresAt,
		LastRefreshAt:        source.LastRefreshAt,
		RefreshFailCount:     source.RefreshFailCount,
		LastRefreshFailureAt: source.LastRefreshFailureAt,
	}
	if strings.TrimSpace(source.UsageSnapshot) != "" {
		if err := json.Unmarshal([]byte(source.UsageSnapshot), &state.UsageSnapshot); err != nil {
			return credentialsession.AuthState{}, fmt.Errorf("decode usage snapshot: %w", err)
		}
	}
	return credentialsession.NormalizeAuthState(credentialsession.KindChatGPT, state), nil
}

func dropLegacyCredentialStorage(tx *gorm.DB) error {
	statements := []string{
		`DROP INDEX IF EXISTS idx_provider_credentials_binding_account_id`,
		`DROP TABLE IF EXISTS provider_auth_states`,
		`DROP TABLE IF EXISTS provider_credentials`,
		`ALTER TABLE provider_api_types DROP COLUMN api_key`,
		`ALTER TABLE providers DROP COLUMN api_key`,
		`ALTER TABLE providers DROP COLUMN credential_type`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("remove provider-owned credential storage with %q: %w", statement, err)
		}
	}
	return nil
}

func validateCredentialSessionSchema(tx *gorm.DB) error {
	for _, table := range []string{"credential_sessions", "route_target_credentials", "credential_session_migrations"} {
		if !tx.Migrator().HasTable(table) {
			return fmt.Errorf("credential session migration missing table %q", table)
		}
	}
	if tx.Migrator().HasColumn("credential_sessions", "vendor") {
		return fmt.Errorf("credential session schema still contains obsolete vendor ownership")
	}
	if tx.Migrator().HasColumn("providers", "api_key") ||
		tx.Migrator().HasColumn("providers", "credential_type") ||
		tx.Migrator().HasColumn("provider_api_types", "api_key") ||
		tx.Migrator().HasTable("provider_credentials") ||
		tx.Migrator().HasTable("provider_auth_states") {
		return fmt.Errorf("credential session migration left provider-owned credential storage")
	}
	return nil
}

func finalizePendingStaticSubjects(db *gorm.DB, clock internalClock, signer StaticCredentialSubjectSigner) error {
	if db == nil || clock == nil || signer == nil {
		return fmt.Errorf("finalize static credential subjects requires database, clock, and signer")
	}
	return db.Connection(func(connection *gorm.DB) (resultErr error) {
		transaction := connection.Session(&gorm.Session{SkipDefaultTransaction: true})
		rollbackConnection := connection.WithContext(context.Background()).Session(&gorm.Session{SkipDefaultTransaction: true})
		if err := transaction.Exec("BEGIN IMMEDIATE").Error; err != nil {
			return fmt.Errorf("begin static credential subject finalization: %w", err)
		}
		committed := false
		defer func() {
			if committed {
				return
			}
			// database/sql considers a Tx finished after Commit is attempted, while
			// SQLite keeps a deferred-constraint failure active. Owning BEGIN/COMMIT
			// on this pinned connection preserves the rollback needed in that case.
			if err := rollbackConnection.Exec("ROLLBACK").Error; err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("rollback static credential subject finalization: %w", err))
			}
		}()

		var sessions []credentialsession.Session
		if err := transaction.Where("kind = ? AND subject_kind = ?", credentialsession.KindAPIKey, credentialsession.SubjectPending).
			Order("id ASC").Find(&sessions).Error; err != nil {
			return fmt.Errorf("list pending static credential subjects: %w", err)
		}
		for index := range sessions {
			subject, err := staticSubject(sessions[index].SecretData, signer)
			if err != nil {
				return fmt.Errorf("finalize subject for credential session %q: %w", sessions[index].ID, err)
			}
			result := transaction.Model(&credentialsession.Session{}).
				Where("id = ? AND subject_kind = ?", sessions[index].ID, credentialsession.SubjectPending).
				Updates(map[string]any{
					"subject_kind":        subject.Kind,
					"subject_value":       append([]byte(nil), subject.Value...),
					"subject_key_version": subject.KeyVersion,
					"updated_at":          clock.Now().UTC(),
				})
			if result.Error != nil {
				return fmt.Errorf("finalize subject for credential session %q: %w", sessions[index].ID, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("finalize subject for credential session %q: concurrent state change", sessions[index].ID)
			}
		}
		if err := transaction.Exec("COMMIT").Error; err != nil {
			return fmt.Errorf("commit static credential subject finalization: %w", err)
		}
		committed = true
		return nil
	})
}
