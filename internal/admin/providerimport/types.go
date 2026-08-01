package providerimport

import (
	"context"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const (
	providerImportActionCreate                               = "create"
	providerImportActionUpdate                               = "update"
	providerImportOutcomeCreated ProviderImportCommitOutcome = "created"
	providerImportOutcomeUpdated ProviderImportCommitOutcome = "updated"
	providerImportFallbackID                                 = "chatgpt-account"
	MaxProviderImportBodySize                                = 5 << 20
	MaxRequestBodySize                                       = 1 << 20
	DefaultWeight                                            = defaults.ProviderWeight
	DefaultProviderMaxRetries                                = defaults.ProviderMaxRetries
	ContentTypeJSON                                          = "application/json"
	ErrCodeValidation                                        = "VALIDATION_ERROR"
	ErrCodeInternal                                          = "INTERNAL_ERROR"
	ErrCodeConflict                                          = "CONFLICT"
	providerImportRetryAfter                                 = "60"
	// Draft admission happens after decoding, so cap transient bodies that have
	// not yet entered the service's aggregate draft quota.
	maxPendingProviderImportBodyBytes      = 30 << 20
	maxConcurrentProviderImportBodyReads   = maxPendingProviderImportBodyBytes / MaxProviderImportBodySize
	maxProviderImportSelections            = 500
	maxProviderImportNameCharacters        = 200
	maxProviderImportIdentifierCharacters  = 200
	maxProviderImportCandidateIDCharacters = 128
	maxProviderImportRoutingValue          = 1_000_000
	maxProviderImportRetryCount            = 10
	maxProviderImportBackoffMultiplier     = 10.0
	maxProviderImportGeneratedIDBaseLength = 180
	maxProviderImportEmailCharacters       = 320
	maxProviderImportAccountIDCharacters   = 256
	maxProviderImportPlanTypeCharacters    = 64
	maxProviderImportWarningCodeCharacters = 100
	maxProviderImportMessageCharacters     = 500
	providerImportBodyReadTimeout          = 30 * time.Second
)

// DraftService owns short-lived credential-bearing drafts. The HTTP layer sees
// only opaque IDs and review-safe metadata until an atomic commit is requested.
type DraftService interface {
	PreviewSub2APIChatGPTImport(raw []byte) (*providerauth.ChatGPTProviderImportPreview, error)
	SealChatGPTProviderImportPreview(importID string, dispositions []providerauth.ChatGPTProviderImportCandidateDisposition) error
	ClaimChatGPTProviderImport(importID string) ([]providerauth.ChatGPTProviderImportCandidate, error)
	ReleaseChatGPTProviderImportClaim(importID string) error
	VerifyChatGPTProviderImportCandidates(ctx context.Context, candidates []providerauth.ChatGPTProviderImportCandidate) error
	InvalidateProviderCredentialSessions(providerIDs []string)
	FinalizeChatGPTProviderImport(importID string) error
	CancelChatGPTProviderImport(importID string) error
}

// ProviderCatalog supplies the immutable provider snapshot used to enrich a
// preview before its disposition is sealed.
type ProviderCatalog interface {
	ListProviders(ctx context.Context) ([]model.Provider, error)
}

// Store is the narrow transactional boundary required by bulk import.
type Store interface {
	WithProviderCredentialMutations(ctx context.Context, providerIDs []string) (ownedCtx context.Context, release func(), err error)
	GetProviderImportReceipt(ctx context.Context, importID string) (*store.ProviderImportReceipt, error)
	ApplyProviderImport(ctx context.Context, bundle *store.ProviderImportBundle) error
}

type Config struct {
	ProviderCatalog ProviderCatalog
	Drafts          DraftService
	Store           Store
	Logger          *zap.Logger
}

// Handler contains the complete provider-import workflow behind a small admin adapter.
type Handler struct {
	store                         ProviderCatalog
	providerImports               DraftService
	providerImportStore           Store
	providerImportReceipts        *providerImportCommitReceiptRegistry
	providerImportReadSlots       chan struct{}
	providerImportReadTimeout     time.Duration
	providerImportSetReadDeadline func(http.ResponseWriter, time.Time) error
	logger                        *zap.Logger
}

func NewHandler(cfg Config) *Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		store:                         cfg.ProviderCatalog,
		providerImports:               cfg.Drafts,
		providerImportStore:           cfg.Store,
		providerImportReceipts:        newProviderImportCommitReceiptRegistry(nil, providerImportCommitReceiptTTL),
		providerImportReadSlots:       make(chan struct{}, maxConcurrentProviderImportBodyReads),
		providerImportReadTimeout:     providerImportBodyReadTimeout,
		providerImportSetReadDeadline: setHTTPResponseReadDeadline,
		logger:                        logger,
	}
}

// ProviderImportPreviewItem deliberately contains only review-safe metadata.
// Credential material stays in the opaque server-side draft until commit.
type ProviderImportPreviewItem struct {
	CandidateID          string                                           `json:"candidate_id"`
	SourceIndex          int                                              `json:"source_index"`
	Status               providerauth.ChatGPTProviderImportCandidateState `json:"status"`
	Name                 string                                           `json:"name"`
	ProviderID           string                                           `json:"provider_id"`
	Email                string                                           `json:"email,omitempty"`
	AccountID            string                                           `json:"account_id,omitempty"`
	PlanType             string                                           `json:"plan_type,omitempty"`
	ExpiresAt            *time.Time                                       `json:"expires_at,omitempty"`
	Priority             int                                              `json:"priority"`
	Concurrency          int                                              `json:"concurrency"`
	ExistingProviderID   string                                           `json:"existing_provider_id,omitempty"`
	ExistingProviderName string                                           `json:"existing_provider_name,omitempty"`
	DefaultSelected      bool                                             `json:"default_selected"`
	Message              string                                           `json:"message,omitempty"`
	Warnings             []providerauth.ChatGPTProviderImportWarning      `json:"warnings"`
}

type ProviderImportPreviewResponse struct {
	ImportID       string                                      `json:"import_id"`
	ExpiresAt      time.Time                                   `json:"expires_at"`
	CreateDefaults ProviderImportCreateDefaults                `json:"create_defaults"`
	Items          []ProviderImportPreviewItem                 `json:"items"`
	Summary        providerauth.ChatGPTProviderImportSummary   `json:"summary"`
	Warnings       []providerauth.ChatGPTProviderImportWarning `json:"warnings"`
}

// ProviderImportCreateDefaults carries server-owned defaults once per preview.
// Keeping non-source settings out of candidate rows avoids duplicating policy for
// large imports and gives the UI one authoritative bulk-edit starting point.
type ProviderImportCreateDefaults struct {
	Weight     int                 `json:"weight"`
	MaxRetries int                 `json:"max_retries"`
	Backoff    model.BackoffPolicy `json:"backoff"`
}

func defaultProviderImportCreateSettings() ProviderImportCreateDefaults {
	return ProviderImportCreateDefaults{
		Weight:     DefaultWeight,
		MaxRetries: DefaultProviderMaxRetries,
		Backoff: model.BackoffPolicy{
			InitialDelay: model.Duration(defaults.BackoffInitialDelay),
			MaxDelay:     model.Duration(defaults.BackoffMaxDelay),
			Multiplier:   defaults.BackoffMultiplier,
		},
	}
}

type ProviderImportCommitRequest struct {
	GroupID *string                    `json:"group_id"`
	Items   []ProviderImportCommitItem `json:"items"`
}

type ProviderImportCommitItem struct {
	CandidateID string               `json:"candidate_id"`
	Action      string               `json:"action"`
	ProviderID  string               `json:"provider_id,omitempty"`
	Name        string               `json:"name,omitempty"`
	Priority    int                  `json:"priority,omitempty"`
	Weight      *int                 `json:"weight,omitempty"`
	Concurrency int                  `json:"concurrency,omitempty"`
	MaxRetries  *int                 `json:"max_retries,omitempty"`
	Backoff     *model.BackoffPolicy `json:"backoff,omitempty"`
}

type ProviderImportCommitSummary struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

type ProviderImportCommitOutcome string

type ProviderImportCommitResultItem struct {
	CandidateID string                      `json:"candidate_id"`
	Outcome     ProviderImportCommitOutcome `json:"outcome"`
	ProviderID  string                      `json:"provider_id"`
	Name        string                      `json:"name,omitempty"`
}

type ProviderImportCommitResponse struct {
	ImportID string                           `json:"import_id"`
	Summary  ProviderImportCommitSummary      `json:"summary"`
	Items    []ProviderImportCommitResultItem `json:"items"`
}

type providerImportBinding struct {
	provider *model.Provider
	version  int64
}
