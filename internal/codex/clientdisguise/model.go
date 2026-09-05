// Package clientdisguise owns persistent virtual login identities and observed
// client profiles. Authentication authority deliberately remains a separate domain.
package clientdisguise

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

const (
	ModeAuto            = "auto"
	ModePinned          = "pinned"
	UnknownExclude      = "exclude"
	UnknownAllowCurrent = "allow_current"
)

var (
	ErrInvalid           = errors.New("invalid client disguise input")
	ErrNotFound          = errors.New("client disguise record not found")
	ErrConflict          = errors.New("client disguise import conflict")
	ErrCandidateExcluded = errors.New("client disguise candidate excluded")
	ErrAccountChanged    = errors.New("client disguise login account changed")
)

type Policy struct {
	Enabled         bool   `json:"enabled"`
	MatchPlatform   *bool  `json:"match_platform,omitempty"`
	UnknownPlatform string `json:"unknown_platform"`
}

func (p Policy) PlatformMatching() bool { return p.MatchPlatform == nil || *p.MatchPlatform }
func (p Policy) Validate() error {
	if p.UnknownPlatform != "" && p.UnknownPlatform != UnknownExclude && p.UnknownPlatform != UnknownAllowCurrent {
		return invalid("unknown platform policy %q", p.UnknownPlatform)
	}
	return nil
}

type Tuple struct {
	ClientType string `json:"client_type"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
}

func (t Tuple) Valid() bool {
	return (t.ClientType == "desktop" || t.ClientType == "tui" || t.ClientType == "cli") &&
		(t.Platform == "windows" || t.Platform == "linux" || t.Platform == "macos") &&
		(t.Arch == "amd64" || t.Arch == "arm64")
}

type Features struct {
	UserAgent     string            `json:"user_agent"`
	Originator    string            `json:"originator"`
	ClientVersion string            `json:"client_version"`
	DesktopBuild  string            `json:"desktop_build"`
	OSVersion     string            `json:"os_version"`
	Headers       map[string]string `json:"headers,omitempty"`
}

type ProfileRevision struct {
	EvidenceKind  string    `json:"evidence_kind,omitempty"`
	SourceURL     string    `json:"source_url,omitempty"`
	ID            string    `json:"id" gorm:"primaryKey"`
	Tuple         Tuple     `json:"tuple" gorm:"serializer:json"`
	ClientVersion string    `json:"client_version"`
	Features      Features  `json:"features" gorm:"serializer:json"`
	SourceID      string    `json:"source_id"`
	CapturedAt    time.Time `json:"captured_at"`
	CreatedAt     time.Time `json:"created_at"`
}

func (ProfileRevision) TableName() string { return "client_disguise_profile_revisions" }

// AccountBasis survives a reauthentication placeholder restore, but must never
// be read as proof that a credential is authenticated.
type AccountBasis struct {
	Kind       string `json:"kind"`
	Value      []byte `json:"value,omitempty"`
	KeyVersion string `json:"key_version,omitempty"`
}

func (a AccountBasis) Resolved() bool {
	return (a.Kind == "account" || a.Kind == "keyed_digest") && len(a.Value) > 0
}
func (a AccountBasis) Equal(b AccountBasis) bool {
	return a.Kind == b.Kind && a.KeyVersion == b.KeyVersion && bytes.Equal(a.Value, b.Value)
}

type LoginIdentity struct {
	CredentialSessionID string       `json:"credential_session_id" gorm:"primaryKey"`
	GenerationID        string       `json:"generation_id" gorm:"uniqueIndex"`
	DeviceID            string       `json:"device_id" gorm:"uniqueIndex"`
	AccountBasis        AccountBasis `json:"account_basis" gorm:"serializer:json"`
	CreatedAt           time.Time    `json:"created_at"`
}

func (LoginIdentity) TableName() string { return "client_disguise_login_identities" }

type LoginHistory struct {
	GenerationID string        `json:"generation_id" gorm:"primaryKey"`
	Identity     LoginIdentity `json:"identity" gorm:"serializer:json"`
}

func (LoginHistory) TableName() string { return "client_disguise_login_history" }

type ProfileBinding struct {
	CredentialSessionID   string            `json:"credential_session_id" gorm:"primaryKey"`
	Tuple                 Tuple             `json:"tuple" gorm:"serializer:json"`
	Mode                  string            `json:"mode"`
	RevisionID            string            `json:"revision_id"`
	ReferenceSourceID     string            `json:"reference_source_id"`
	TransportSampleID     string            `json:"transport_sample_id"`
	RemapCacheKeys        bool              `json:"remap_cache_keys"`
	TelemetryPathMappings map[string]string `json:"telemetry_path_mappings,omitempty" gorm:"serializer:json"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

func (ProfileBinding) TableName() string { return "client_disguise_profile_bindings" }

type Sample struct {
	ID            string    `json:"id" gorm:"primaryKey"`
	SourceID      string    `json:"source_id"`
	CapturedAt    time.Time `json:"captured_at"`
	Tuple         Tuple     `json:"tuple" gorm:"serializer:json"`
	ClientVersion string    `json:"client_version"`
	Features      Features  `json:"features" gorm:"serializer:json"`
}

func (Sample) TableName() string { return "client_disguise_samples" }

type LearnResult struct {
	Revision         ProfileRevision `json:"revision"`
	Created          bool            `json:"created"`
	AdvancedSessions []string        `json:"advanced_sessions"`
}
type ReferenceSource struct {
	ID               string `json:"id" gorm:"primaryKey"`
	Name             string `json:"name"`
	ClientIdentityID string `json:"client_identity_id"`
}

func (ReferenceSource) TableName() string { return "client_disguise_reference_sources" }

// Transport observations are independent of application features. Config is an
// explicitly supported transport adapter payload, never inferred from the UA.
type TransportSample struct {
	ID          string          `json:"id" gorm:"primaryKey"`
	SourceID    string          `json:"source_id"`
	CapturedAt  time.Time       `json:"captured_at"`
	Name        string          `json:"name"`
	TLSProfile  string          `json:"tls_profile"`
	HTTPProfile string          `json:"http_profile"`
	Config      json.RawMessage `json:"config" gorm:"type:blob"`
}

func (TransportSample) TableName() string { return "client_disguise_transport_samples" }

type TargetSnapshot struct {
	Policy    Policy           `json:"policy"`
	Login     LoginIdentity    `json:"login"`
	Binding   ProfileBinding   `json:"binding"`
	Profile   ProfileRevision  `json:"profile"`
	Transport *TransportSample `json:"transport,omitempty"`
}

type MappingKey struct {
	GenerationID     string `json:"generation_id" gorm:"primaryKey"`
	ClientIdentityID string `json:"client_identity_id" gorm:"primaryKey"`
	Namespace        string `json:"namespace" gorm:"primaryKey"`
	Original         string `json:"original" gorm:"primaryKey"`
}
type Mapping struct {
	MappingKey `gorm:"embedded"`
	Mapped     string `json:"mapped" gorm:"uniqueIndex:idx_disguise_mapped"`
}

func (Mapping) TableName() string { return "client_disguise_mappings" }

// ProfileTrack separates the latest observation time from the chosen immutable
// revision, because duplicate samples advance ordering without changing features.
type ProfileTrack struct {
	SourceID      string    `json:"source_id" gorm:"primaryKey"`
	ClientType    string    `json:"client_type" gorm:"primaryKey"`
	Platform      string    `json:"platform" gorm:"primaryKey"`
	Arch          string    `json:"arch" gorm:"primaryKey"`
	ClientVersion string    `json:"client_version"`
	RevisionID    string    `json:"revision_id"`
	CapturedAt    time.Time `json:"captured_at"`
}

func (ProfileTrack) TableName() string { return "client_disguise_profile_tracks" }
func (t ProfileTrack) Tuple() Tuple {
	return Tuple{ClientType: t.ClientType, Platform: t.Platform, Arch: t.Arch}
}

type Snapshot struct {
	Tracks           []ProfileTrack    `json:"tracks"`
	Logins           []LoginIdentity   `json:"logins"`
	LoginHistory     []LoginHistory    `json:"login_history"`
	Bindings         []ProfileBinding  `json:"bindings"`
	Profiles         []ProfileRevision `json:"profiles"`
	Samples          []Sample          `json:"samples"`
	References       []ReferenceSource `json:"references"`
	Mappings         []Mapping         `json:"mappings"`
	TransportSamples []TransportSample `json:"transport_samples"`
}
