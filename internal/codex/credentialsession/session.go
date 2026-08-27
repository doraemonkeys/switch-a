// Package credentialsession owns upstream credential identity, secret rotation,
// and authentication lifecycle independently from route targets.
package credentialsession

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	credentialSubjectCodec  = "credential-session-subject/v1"
	staticSubjectDigestSize = 32
)

var (
	ErrNotFound            = errors.New("credential session not found")
	ErrVersionConflict     = errors.New("credential session version conflict")
	ErrSessionReferenced   = errors.New("credential session is still referenced")
	ErrSubjectPending      = errors.New("credential session subject is pending")
	ErrInvalidSession      = errors.New("invalid credential session")
	ErrInvalidRouteBinding = errors.New("invalid route credential binding")
)

// Kind identifies how a session proves and applies its upstream credential.
type Kind string

const (
	KindAPIKey  Kind = "api_key"
	KindChatGPT Kind = "chatgpt"
)

func IsValidKind(kind Kind) bool {
	return kind == KindAPIKey || kind == KindChatGPT
}

// SubjectKind prevents an account identifier and a keyed digest from being
// interpreted as interchangeable persistence formats.
type SubjectKind string

const (
	SubjectPending     SubjectKind = "pending"
	SubjectAccount     SubjectKind = "account"
	SubjectKeyedDigest SubjectKind = "keyed_digest"
)

// Subject is the stable security subject of one credential session. Pending is
// valid only while all Authority-dependent features are disabled.
type Subject struct {
	Kind       SubjectKind `json:"kind"`
	Value      []byte      `json:"value,omitempty"`
	KeyVersion string      `json:"key_version,omitempty"`
}

func PendingSubject() Subject {
	return Subject{Kind: SubjectPending}
}

func AccountSubject(accountID string) (Subject, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return Subject{}, fmt.Errorf("%w: account subject is blank", ErrInvalidSession)
	}
	return Subject{Kind: SubjectAccount, Value: []byte(accountID)}, nil
}

func KeyedDigestSubject(version string, digest []byte) (Subject, error) {
	version = strings.TrimSpace(version)
	if version == "" || len(digest) != staticSubjectDigestSize {
		return Subject{}, fmt.Errorf("%w: keyed subject requires a version and %d-byte digest", ErrInvalidSession, staticSubjectDigestSize)
	}
	return Subject{Kind: SubjectKeyedDigest, Value: append([]byte(nil), digest...), KeyVersion: version}, nil
}

func (s Subject) Validate() error {
	switch s.Kind {
	case SubjectPending:
		if len(s.Value) != 0 || s.KeyVersion != "" {
			return fmt.Errorf("%w: pending subject carries resolved data", ErrInvalidSession)
		}
	case SubjectAccount:
		accountID := string(s.Value)
		if accountID == "" || strings.TrimSpace(accountID) != accountID || s.KeyVersion != "" {
			return fmt.Errorf("%w: invalid account subject", ErrInvalidSession)
		}
	case SubjectKeyedDigest:
		if strings.TrimSpace(s.KeyVersion) == "" || len(s.Value) != staticSubjectDigestSize {
			return fmt.Errorf("%w: invalid keyed subject", ErrInvalidSession)
		}
	default:
		return fmt.Errorf("%w: unknown subject kind %q", ErrInvalidSession, s.Kind)
	}
	return nil
}

func (s Subject) Resolved() bool {
	return s.Validate() == nil && s.Kind != SubjectPending
}

func (s Subject) Clone() Subject {
	clone := s
	clone.Value = append([]byte(nil), s.Value...)
	return clone
}

// AuthStatus is session lifecycle state, not route-target health.
type AuthStatus string

const (
	AuthStatusNotConnected   AuthStatus = "not_connected"
	AuthStatusActive         AuthStatus = "active"
	AuthStatusReauthRequired AuthStatus = "reauth_required"
)

func IsValidAuthStatus(status AuthStatus) bool {
	switch status {
	case AuthStatusNotConnected, AuthStatusActive, AuthStatusReauthRequired:
		return true
	default:
		return false
	}
}

func DefaultAuthStatus(kind Kind) AuthStatus {
	if kind == KindChatGPT {
		return AuthStatusNotConnected
	}
	return AuthStatusActive
}

type UsageWindow struct {
	UsedPercent   float64    `json:"used_percent"`
	WindowSeconds int64      `json:"window_seconds"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
}

type UsageSnapshot struct {
	FetchedAt *time.Time   `json:"fetched_at,omitempty"`
	PlanType  string       `json:"plan_type,omitempty"`
	FiveHour  *UsageWindow `json:"five_hour,omitempty"`
	OneWeek   *UsageWindow `json:"one_week,omitempty"`
}

// AuthState is embedded in Session so every lifecycle mutation is naturally
// scoped to SessionID rather than to a route target.
type AuthState struct {
	Status               AuthStatus     `gorm:"column:auth_status;type:text;not null;default:not_connected;index" json:"status"`
	StatusReason         string         `gorm:"column:auth_status_reason;type:text;not null;default:''" json:"status_reason,omitempty"`
	LastError            string         `gorm:"column:auth_last_error;type:text;not null;default:''" json:"last_error,omitempty"`
	LastTransitionAt     *time.Time     `gorm:"column:auth_last_transition_at" json:"last_transition_at,omitempty"`
	Email                string         `gorm:"column:auth_email;type:text;not null;default:''" json:"email,omitempty"`
	AccountID            string         `gorm:"column:auth_account_id;type:text;not null;default:'';index" json:"account_id,omitempty"`
	PlanType             string         `gorm:"column:auth_plan_type;type:text;not null;default:''" json:"plan_type,omitempty"`
	ExpiresAt            *time.Time     `gorm:"column:auth_expires_at" json:"expires_at,omitempty"`
	LastRefreshAt        *time.Time     `gorm:"column:auth_last_refresh_at" json:"last_refresh_at,omitempty"`
	UsageSnapshot        *UsageSnapshot `gorm:"column:auth_usage_snapshot;serializer:json" json:"usage_snapshot,omitempty"`
	RefreshFailCount     int            `gorm:"column:auth_refresh_fail_count;not null;default:0" json:"refresh_fail_count,omitempty"`
	LastRefreshFailureAt *time.Time     `gorm:"column:auth_last_refresh_failure_at" json:"last_refresh_failure_at,omitempty"`
}

func (s AuthState) Clone() AuthState {
	clone := s
	clone.LastTransitionAt = cloneTime(s.LastTransitionAt)
	clone.ExpiresAt = cloneTime(s.ExpiresAt)
	clone.LastRefreshAt = cloneTime(s.LastRefreshAt)
	clone.LastRefreshFailureAt = cloneTime(s.LastRefreshFailureAt)
	clone.UsageSnapshot = cloneUsageSnapshot(s.UsageSnapshot)
	return clone
}

func NormalizeAuthState(kind Kind, state AuthState) AuthState {
	if !IsValidAuthStatus(state.Status) {
		state.Status = DefaultAuthStatus(kind)
	}
	state.StatusReason = strings.TrimSpace(state.StatusReason)
	state.LastError = strings.TrimSpace(state.LastError)
	state.Email = strings.TrimSpace(state.Email)
	state.AccountID = strings.TrimSpace(state.AccountID)
	state.PlanType = strings.TrimSpace(state.PlanType)
	if state.PlanType == "" && state.UsageSnapshot != nil {
		state.PlanType = strings.TrimSpace(state.UsageSnapshot.PlanType)
	}
	if state.RefreshFailCount < 0 {
		state.RefreshFailCount = 0
	}
	return state
}

// Session is the aggregate root for independently rotating secret material.
type Session struct {
	ID                string      `gorm:"column:id;primaryKey;type:text" json:"id"`
	Vendor            string      `gorm:"column:vendor;type:text;not null;index" json:"vendor"`
	Kind              Kind        `gorm:"column:kind;type:text;not null;index" json:"kind"`
	SecretData        string      `gorm:"column:secret_data;type:text;not null" json:"-"`
	Version           int64       `gorm:"column:version;not null" json:"version"`
	SubjectKind       SubjectKind `gorm:"column:subject_kind;type:text;not null;index" json:"subject_kind"`
	SubjectValue      []byte      `gorm:"column:subject_value;type:blob" json:"-"`
	SubjectKeyVersion string      `gorm:"column:subject_key_version;type:text;not null;default:''" json:"subject_key_version,omitempty"`
	AuthState         AuthState   `gorm:"embedded" json:"auth_state"`
	CreatedAt         time.Time   `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt         time.Time   `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (Session) TableName() string { return "credential_sessions" }

func (s *Session) Subject() Subject {
	if s == nil {
		return Subject{}
	}
	return Subject{Kind: s.SubjectKind, Value: append([]byte(nil), s.SubjectValue...), KeyVersion: s.SubjectKeyVersion}
}

func (s *Session) SetSubject(subject Subject) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrInvalidSession)
	}
	if err := subject.Validate(); err != nil {
		return err
	}
	s.SubjectKind = subject.Kind
	s.SubjectValue = append([]byte(nil), subject.Value...)
	s.SubjectKeyVersion = subject.KeyVersion
	return nil
}

func (s *Session) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrInvalidSession)
	}
	if strings.TrimSpace(s.ID) == "" || !IsValidKind(s.Kind) {
		return fmt.Errorf("%w: id and kind are required", ErrInvalidSession)
	}
	if strings.TrimSpace(s.SecretData) == "" {
		return fmt.Errorf("%w: secret data is blank", ErrInvalidSession)
	}
	if s.Version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalidSession)
	}
	if err := s.Subject().Validate(); err != nil {
		return err
	}
	if err := validateSubjectForKind(s.Kind, s.Subject()); err != nil {
		return err
	}
	if err := validateAuthStateForSubject(s.Kind, s.Subject(), s.AuthState); err != nil {
		return err
	}
	return nil
}

func validateSubjectForKind(kind Kind, subject Subject) error {
	switch kind {
	case KindAPIKey:
		if subject.Kind != SubjectPending && subject.Kind != SubjectKeyedDigest {
			return fmt.Errorf("%w: api-key session requires pending or keyed-digest subject", ErrInvalidSession)
		}
	case KindChatGPT:
		if subject.Kind != SubjectAccount && subject.Kind != SubjectPending {
			return fmt.Errorf("%w: chatgpt session requires account or recovery-pending subject", ErrInvalidSession)
		}
	}
	return nil
}

func validateAuthStateForSubject(kind Kind, subject Subject, state AuthState) error {
	if kind != KindChatGPT {
		return nil
	}
	if subject.Kind == SubjectPending {
		if state.Status != AuthStatusReauthRequired {
			return fmt.Errorf("%w: pending chatgpt subject is restricted to reauthentication recovery", ErrInvalidSession)
		}
		return nil
	}
	accountID := strings.TrimSpace(state.AccountID)
	if accountID != "" && accountID != strings.TrimSpace(string(subject.Value)) {
		return fmt.Errorf("%w: chatgpt auth account does not match its subject", ErrInvalidSession)
	}
	return nil
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	clone := *s
	clone.SubjectValue = append([]byte(nil), s.SubjectValue...)
	clone.AuthState = s.AuthState.Clone()
	return &clone
}

// Snapshot is the immutable input consumed by authentication and identity
// resolution for one route-target/API-type candidate.
type Snapshot struct {
	SessionID  string
	Vendor     string
	Kind       Kind
	SecretData string
	Version    int64
	Subject    Subject
	AuthState  AuthState
}

func (s *Session) Snapshot() (Snapshot, error) {
	if err := s.Validate(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		SessionID:  s.ID,
		Vendor:     s.Vendor,
		Kind:       s.Kind,
		SecretData: s.SecretData,
		Version:    s.Version,
		Subject:    s.Subject().Clone(),
		AuthState:  s.AuthState.Clone(),
	}, nil
}

func (s Snapshot) RequireResolvedSubject() error {
	if !s.Subject.Resolved() {
		return fmt.Errorf("%w: session %q", ErrSubjectPending, s.SessionID)
	}
	return nil
}

// RouteBinding makes the credential used by each route/API pair explicit.
type RouteBinding struct {
	RouteTargetID string    `gorm:"column:route_target_id;primaryKey;type:text" json:"route_target_id"`
	APIType       string    `gorm:"column:api_type;primaryKey;type:text" json:"api_type"`
	SessionID     string    `gorm:"column:session_id;type:text;not null;index" json:"session_id"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (RouteBinding) TableName() string { return "route_target_credentials" }

func (b RouteBinding) Validate() error {
	if strings.TrimSpace(b.RouteTargetID) == "" || strings.TrimSpace(b.APIType) == "" || strings.TrimSpace(b.SessionID) == "" {
		return ErrInvalidRouteBinding
	}
	return nil
}

type RouteSnapshot struct {
	RouteTargetID string   `json:"route_target_id"`
	APIType       string   `json:"api_type"`
	Credential    Snapshot `json:"credential_session"`
}

// StaticSubjectInput is the one canonical byte encoding signed by KR1. Length
// prefixes keep values unambiguous without assigning delimiter semantics.
func StaticSubjectInput(vendor string, kind Kind, secret string) ([]byte, error) {
	vendor = strings.TrimSpace(vendor)
	if kind != KindAPIKey || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("%w: static subject requires api_key kind and secret", ErrInvalidSession)
	}
	parts := [][]byte{[]byte(credentialSubjectCodec), []byte(vendor), []byte(kind), []byte(secret)}
	total := 0
	for _, part := range parts {
		total += 4 + len(part)
	}
	encoded := make([]byte, 0, total)
	for _, part := range parts {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		encoded = append(encoded, size[:]...)
		encoded = append(encoded, part...)
	}
	return encoded, nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneUsageSnapshot(snapshot *UsageSnapshot) *UsageSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.FetchedAt = cloneTime(snapshot.FetchedAt)
	if snapshot.FiveHour != nil {
		window := *snapshot.FiveHour
		window.ResetAt = cloneTime(snapshot.FiveHour.ResetAt)
		clone.FiveHour = &window
	}
	if snapshot.OneWeek != nil {
		window := *snapshot.OneWeek
		window.ResetAt = cloneTime(snapshot.OneWeek.ResetAt)
		clone.OneWeek = &window
	}
	return &clone
}
