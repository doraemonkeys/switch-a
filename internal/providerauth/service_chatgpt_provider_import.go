package providerauth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

const (
	chatGPTProviderImportSessionTTL = 15 * time.Minute
	maxGeneratedIDAttempts          = 8
)

const (
	ChatGPTProviderImportWarningRateMultiplierIgnored = "rate_multiplier_ignored"
	ChatGPTProviderImportWarningProxiesIgnored        = "proxies_ignored"
	ChatGPTProviderImportWarningAutoPauseIgnored      = "auto_pause_on_expired_ignored"
	ChatGPTProviderImportWarningTokenExpired          = "token_expired_refresh_required"
	ChatGPTProviderImportWarningDuplicateAccount      = "duplicate_account"
	ChatGPTProviderImportWarningInvalidAccount        = "invalid_account"
	ChatGPTProviderImportWarningUnsupportedAccount    = "unsupported_account"
	ChatGPTProviderImportWarningAccountIDMismatch     = "account_id_mismatch"
	ChatGPTProviderImportWarningUsageMetadataInvalid  = "usage_metadata_invalid"
)

var (
	ErrChatGPTProviderImportInvalidDocument   = errors.New("invalid chatgpt provider import document")
	ErrChatGPTProviderImportNotFound          = errors.New("chatgpt provider import not found")
	ErrChatGPTProviderImportExpired           = errors.New("chatgpt provider import expired")
	ErrChatGPTProviderImportCandidateNotFound = errors.New("chatgpt provider import candidate not found")
	ErrChatGPTProviderImportInvalidCandidate  = errors.New("invalid chatgpt provider import candidate")
	ErrChatGPTProviderImportPreviewNotSealed  = errors.New("chatgpt provider import preview not sealed")
	ErrChatGPTProviderImportPreviewSealed     = errors.New("chatgpt provider import preview already sealed")
	ErrChatGPTProviderImportInProgress        = errors.New("chatgpt provider import is in progress")
	ErrChatGPTProviderImportNotClaimed        = errors.New("chatgpt provider import is not claimed")
	ErrChatGPTProviderImportCapacityExceeded  = errors.New("chatgpt provider import capacity exceeded")
)

// ChatGPTProviderImportCandidateState describes structural source readiness.
// Existing is reserved for the admin/store enrichment layer because provider
// ownership cannot be inferred safely inside the auth parser.
type ChatGPTProviderImportCandidateState string

const (
	ChatGPTProviderImportCandidateStateReady       ChatGPTProviderImportCandidateState = "ready"
	ChatGPTProviderImportCandidateStateExisting    ChatGPTProviderImportCandidateState = "existing"
	ChatGPTProviderImportCandidateStateDuplicate   ChatGPTProviderImportCandidateState = "duplicate"
	ChatGPTProviderImportCandidateStateInvalid     ChatGPTProviderImportCandidateState = "invalid"
	ChatGPTProviderImportCandidateStateUnsupported ChatGPTProviderImportCandidateState = "unsupported"
)

// ChatGPTProviderImportWarning is safe to return to an admin client. Messages
// must describe source decisions without echoing imported secret material.
type ChatGPTProviderImportWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ChatGPTProviderImportSummary provides stable counters for the preview UI.
type ChatGPTProviderImportSummary struct {
	Total       int `json:"total"`
	Ready       int `json:"ready"`
	Existing    int `json:"existing"`
	Duplicate   int `json:"duplicate"`
	Invalid     int `json:"invalid"`
	Unsupported int `json:"unsupported"`
}

// ChatGPTProviderImportPreview is the token-free representation of a staged
// import. The opaque identifiers are the only bridge to the server-side draft.
type ChatGPTProviderImportPreview struct {
	ImportID  string                             `json:"import_id"`
	ExpiresAt time.Time                          `json:"expires_at"`
	Items     []ChatGPTProviderImportPreviewItem `json:"items"`
	Summary   ChatGPTProviderImportSummary       `json:"summary"`
	Warnings  []ChatGPTProviderImportWarning     `json:"warnings"`
}

// ChatGPTProviderImportPreviewItem exposes identity and routing defaults but
// deliberately has no field capable of serializing token material.
type ChatGPTProviderImportPreviewItem struct {
	CandidateID string                              `json:"candidate_id"`
	SourceIndex int                                 `json:"source_index"`
	State       ChatGPTProviderImportCandidateState `json:"state"`
	Name        string                              `json:"name"`
	Concurrency int                                 `json:"concurrency"`
	Priority    int                                 `json:"priority"`
	Auth        *ProviderAuthView                   `json:"auth,omitempty"`
	Warnings    []ChatGPTProviderImportWarning      `json:"warnings"`
}

// ChatGPTProviderImportCandidate is the server-side commit material. Split
// credential and auth-state records are cloned on retrieval and excluded from
// JSON so an accidental handler serialization cannot disclose secrets.
type ChatGPTProviderImportCandidate struct {
	CandidateID string
	SourceIndex int
	State       ChatGPTProviderImportCandidateState
	Name        string
	Concurrency int
	Priority    int
	Credential  *model.ProviderCredential                  `json:"-"`
	AuthState   *model.ProviderAuthState                   `json:"-"`
	Disposition *ChatGPTProviderImportCandidateDisposition `json:"-"`
	Warnings    []ChatGPTProviderImportWarning
}

// ChatGPTProviderImportCandidateDisposition freezes the store-backed decision
// shown in preview. Create IDs remain user-editable, while existing bindings carry
// the exact provider and credential version that commit must compare-and-swap.
type ChatGPTProviderImportCandidateDisposition struct {
	CandidateID                 string
	State                       ChatGPTProviderImportCandidateState
	ExpectedProviderID          string
	ExpectedCredentialVersion   int64
	ExpectedCredentialCreatedAt time.Time
}

type stagedChatGPTProviderImport struct {
	expiresAt  time.Time
	order      []string
	candidates map[string]ChatGPTProviderImportCandidate
	sealed     bool
	claimed    bool
	sizeBytes  int64
}

// PreviewSub2APIChatGPTImport parses every account independently and stages
// valid credential records without contacting OpenAI. A malformed account is
// represented in the preview instead of hiding otherwise importable siblings.
func (s *Service) PreviewSub2APIChatGPTImport(raw []byte) (*ChatGPTProviderImportPreview, error) {
	rawBytes := int64(len(raw))
	if err := s.reserveChatGPTProviderImportCapacity(rawBytes); err != nil {
		return nil, err
	}
	reservationActive := true
	defer func() {
		if reservationActive {
			s.releaseChatGPTProviderImportReservation(rawBytes)
		}
	}()

	now := s.clock.Now()
	parsed, err := s.parseSub2APIChatGPTImport(raw, now)
	if err != nil {
		return nil, err
	}
	importID, err := s.generateOpaqueImportID(nil)
	if err != nil {
		return nil, err
	}

	expiresAt := now.Add(chatGPTProviderImportSessionTTL)
	retainedBytes := max(rawBytes, chatGPTProviderImportSecretBytes(parsed.candidates))
	staged := stagedChatGPTProviderImport{
		expiresAt:  expiresAt,
		order:      make([]string, len(parsed.candidates)),
		candidates: make(map[string]ChatGPTProviderImportCandidate, len(parsed.candidates)),
		sizeBytes:  retainedBytes,
	}
	for index := range parsed.candidates {
		candidate := cloneChatGPTProviderImportCandidate(parsed.candidates[index])
		staged.order[index] = candidate.CandidateID
		staged.candidates[candidate.CandidateID] = candidate
	}

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil, errProviderAuthServiceShutdown
	}
	s.pruneExpiredSessionsLocked(now)
	for attempt := 0; attempt < maxGeneratedIDAttempts; attempt++ {
		if _, exists := s.providerImports[importID]; !exists {
			break
		}
		importID, err = s.generateOpaqueImportID(nil)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	if _, exists := s.providerImports[importID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("generate unique provider import id")
	}
	if err := s.replaceChatGPTProviderImportReservationLocked(rawBytes, retainedBytes); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.providerImports[importID] = staged
	reservationActive = false
	s.syncSessionExpiryTaskLocked(now)
	s.mu.Unlock()

	preview := &ChatGPTProviderImportPreview{
		ImportID:  importID,
		ExpiresAt: expiresAt.UTC(),
		Items:     parsed.items,
		Summary:   summarizeChatGPTProviderImportItems(parsed.items),
		Warnings:  parsed.warnings,
	}
	s.logger.Info("staged chatgpt provider import preview",
		zap.String("import_id", importID),
		zap.Int("total", preview.Summary.Total),
		zap.Int("ready", preview.Summary.Ready),
		zap.Int("duplicate", preview.Summary.Duplicate),
		zap.Int("invalid", preview.Summary.Invalid),
		zap.Int("unsupported", preview.Summary.Unsupported),
	)
	return preview, nil
}

func (s *Service) generateOpaqueImportID(used map[string]struct{}) (string, error) {
	for attempt := 0; attempt < maxGeneratedIDAttempts; attempt++ {
		id := strings.TrimSpace(s.idGenerator.NewID())
		if id == "" {
			continue
		}
		if _, exists := used[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("generate opaque provider import id")
}

// SealChatGPTProviderImportPreview atomically freezes the binding decisions made
// from the provider snapshot used to render preview. A commit must consume these
// sealed expectations instead of reinterpreting the account against newer rows.
func (s *Service) SealChatGPTProviderImportPreview(
	importID string,
	dispositions []ChatGPTProviderImportCandidateDisposition,
) error {
	trimmedImportID := strings.TrimSpace(importID)
	if trimmedImportID == "" {
		return ErrChatGPTProviderImportNotFound
	}

	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	staged, ok := s.providerImports[trimmedImportID]
	if ok && !staged.claimed && !staged.expiresAt.After(now) {
		s.deleteChatGPTProviderImportLocked(trimmedImportID)
		s.syncSessionExpiryTaskLocked(now)
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportExpired, trimmedImportID)
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportNotFound, trimmedImportID)
	}
	if staged.sealed {
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportPreviewSealed, trimmedImportID)
	}
	if len(dispositions) != len(staged.order) {
		return fmt.Errorf(
			"%w: preview dispositions must cover every candidate",
			ErrChatGPTProviderImportInvalidCandidate,
		)
	}

	candidates := make(map[string]ChatGPTProviderImportCandidate, len(staged.candidates))
	for candidateID, candidate := range staged.candidates {
		candidates[candidateID] = cloneChatGPTProviderImportCandidate(candidate)
	}
	seen := make(map[string]struct{}, len(dispositions))
	for _, rawDisposition := range dispositions {
		disposition := rawDisposition
		disposition.CandidateID = strings.TrimSpace(disposition.CandidateID)
		disposition.ExpectedProviderID = strings.TrimSpace(disposition.ExpectedProviderID)
		candidate, exists := candidates[disposition.CandidateID]
		if !exists {
			return fmt.Errorf(
				"%w: %s",
				ErrChatGPTProviderImportCandidateNotFound,
				disposition.CandidateID,
			)
		}
		if _, duplicate := seen[disposition.CandidateID]; duplicate {
			return fmt.Errorf(
				"%w: duplicate disposition for candidate %s",
				ErrChatGPTProviderImportInvalidCandidate,
				disposition.CandidateID,
			)
		}
		if err := validateChatGPTProviderImportDisposition(candidate, disposition); err != nil {
			return err
		}
		seen[disposition.CandidateID] = struct{}{}
		candidate.Disposition = cloneChatGPTProviderImportCandidateDisposition(&disposition)
		candidates[disposition.CandidateID] = candidate
	}

	staged.candidates = candidates
	staged.sealed = true
	s.providerImports[trimmedImportID] = staged
	s.logger.Debug("sealed chatgpt provider import preview", zap.String("import_id", trimmedImportID))
	return nil
}

func validateChatGPTProviderImportDisposition(
	candidate ChatGPTProviderImportCandidate,
	disposition ChatGPTProviderImportCandidateDisposition,
) error {
	if candidate.State != ChatGPTProviderImportCandidateStateReady {
		if disposition.State != candidate.State ||
			disposition.ExpectedProviderID != "" ||
			disposition.ExpectedCredentialVersion != 0 ||
			!disposition.ExpectedCredentialCreatedAt.IsZero() {
			return fmt.Errorf(
				"%w: blocked candidate %s disposition cannot change",
				ErrChatGPTProviderImportInvalidCandidate,
				candidate.CandidateID,
			)
		}
		return nil
	}

	switch disposition.State {
	case ChatGPTProviderImportCandidateStateReady:
		if disposition.ExpectedProviderID != "" ||
			disposition.ExpectedCredentialVersion != 0 ||
			!disposition.ExpectedCredentialCreatedAt.IsZero() {
			return fmt.Errorf(
				"%w: create candidate %s cannot bind an existing provider",
				ErrChatGPTProviderImportInvalidCandidate,
				candidate.CandidateID,
			)
		}
	case ChatGPTProviderImportCandidateStateExisting:
		if disposition.ExpectedProviderID == "" ||
			disposition.ExpectedCredentialVersion < 0 ||
			disposition.ExpectedCredentialCreatedAt.IsZero() {
			return fmt.Errorf(
				"%w: existing candidate %s requires a provider, non-negative credential version, and credential creation time",
				ErrChatGPTProviderImportInvalidCandidate,
				candidate.CandidateID,
			)
		}
	default:
		return fmt.Errorf(
			"%w: ready candidate %s has unsupported preview state %s",
			ErrChatGPTProviderImportInvalidCandidate,
			candidate.CandidateID,
			disposition.State,
		)
	}
	return nil
}

// ApplyChatGPTProviderImportCandidate attaches cloned split records to the final
// provider ID. Routing fields remain caller-owned so UI edits are not overwritten.
func ApplyChatGPTProviderImportCandidate(
	provider *model.Provider,
	candidate ChatGPTProviderImportCandidate,
) error {
	if provider == nil {
		return fmt.Errorf("%w: provider is required", ErrChatGPTProviderImportInvalidCandidate)
	}
	if candidate.State != ChatGPTProviderImportCandidateStateReady {
		return fmt.Errorf(
			"%w: candidate %s is %s",
			ErrChatGPTProviderImportInvalidCandidate,
			candidate.CandidateID,
			candidate.State,
		)
	}
	if candidate.Credential == nil || candidate.AuthState == nil {
		return fmt.Errorf(
			"%w: candidate %s has no importable credential",
			ErrChatGPTProviderImportInvalidCandidate,
			candidate.CandidateID,
		)
	}

	provider.CredentialType = providerCredentialTypeChatGPT
	NormalizeProviderForPersistence(provider)
	provider.Credential = model.NormalizeProviderCredentialRecord(provider.ID, candidate.Credential.Clone())
	provider.AuthState = model.NormalizeProviderAuthStateRecord(
		provider.ID,
		providerCredentialTypeChatGPT,
		candidate.AuthState.Clone(),
	)
	return nil
}

func cloneChatGPTProviderImportCandidate(candidate ChatGPTProviderImportCandidate) ChatGPTProviderImportCandidate {
	cloned := candidate
	cloned.Credential = candidate.Credential.Clone()
	cloned.AuthState = candidate.AuthState.Clone()
	cloned.Disposition = cloneChatGPTProviderImportCandidateDisposition(candidate.Disposition)
	cloned.Warnings = append([]ChatGPTProviderImportWarning{}, candidate.Warnings...)
	return cloned
}

func cloneChatGPTProviderImportCandidateDisposition(
	disposition *ChatGPTProviderImportCandidateDisposition,
) *ChatGPTProviderImportCandidateDisposition {
	if disposition == nil {
		return nil
	}
	cloned := *disposition
	return &cloned
}

func normalizedBindingAccountID(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
