// Package attemptevidence owns the bounded v2 attempt-evidence envelope.
package attemptevidence

import (
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

const (
	EnvelopeVersion         = 2
	SemanticSchemaVersion   = 1
	MaxAttemptEvidenceBytes = 32 << 10
	MaxIdentityBytes        = 512
)

type CredentialPhase string

const (
	CredentialPhasePrimary   CredentialPhase = "primary"
	CredentialPhaseRefreshed CredentialPhase = "refreshed"
)

type ResponseState string

const (
	ResponseStateProbing    ResponseState = "probing"
	ResponseStateForwarding ResponseState = "forwarding"
	ResponseStateDiscarded  ResponseState = "discarded"
)

type MatchTiming string

const (
	MatchTimingProbing    MatchTiming = "probing"
	MatchTimingForwarding MatchTiming = "forwarding"
)

type AlternateOutcome string

const (
	AlternateNotRequested AlternateOutcome = "not_requested"
	AlternateReserved     AlternateOutcome = "reserved"
	AlternateActivated    AlternateOutcome = "activated"
	AlternateUnavailable  AlternateOutcome = "unavailable"
	AlternateFailed       AlternateOutcome = "failed"
	AlternateReleased     AlternateOutcome = "released"
)

type SwitchMode string

const (
	SwitchModeReplacement SwitchMode = "replacement"
	SwitchModeFailover    SwitchMode = "failover"
)

type IdentityFacts struct {
	RequestID       string
	OperationID     string
	ProviderID      string
	LogicalAttempt  uint64
	ProviderAttempt uint64
	CredentialPhase CredentialPhase
}

type ResponseFacts struct {
	ProtocolID             apicontract.ResponseProtocolID
	State                  ResponseState
	MatchTiming            MatchTiming
	BoundaryReason         responseanalysis.BoundaryReason
	ElapsedMilliseconds    uint64
	PeakProbeBytes         uint64
	RawProbeBytes          uint64
	DecodedProbeBytes      uint64
	UpstreamBytesRead      uint64
	ClientBodyBytesWritten uint64
	HeadersCommitted       bool
	VisibleToClient        bool
}

type RuleFacts struct {
	Revision errorrule.Revision
	Winner   errorrule.RuleMatch
	Matches  []errorrule.RuleMatch
}

type RetryFacts struct {
	GlobalAttemptsStarted   uint64
	GlobalAttemptsRemaining uint64
	GlobalAttemptsUnlimited bool
	RuleRetriesScheduled    uint64
	RuleRetryLimit          int
}

type AlternateFacts struct {
	Outcome      AlternateOutcome
	ProviderID   *string
	SwitchMode   *SwitchMode
	SwitchReason *errorrule.SwitchReason
}

type HealthFacts struct {
	Assessment    errorrule.HealthAssessment
	CircuitOpened bool
}

type Facts struct {
	Identity  IdentityFacts
	Response  ResponseFacts
	Rule      RuleFacts
	Retry     RetryFacts
	Alternate AlternateFacts
	Decision  errorrule.Decision
	Health    HealthFacts
}

type SemanticError struct {
	SchemaVersion int       `json:"schema_version"`
	Identity      Identity  `json:"identity"`
	Response      Response  `json:"response"`
	Rule          Rule      `json:"rule"`
	Retry         Retry     `json:"retry"`
	Alternate     Alternate `json:"alternate"`
	Decision      Decision  `json:"decision"`
	Health        Health    `json:"health"`
}

type Identity struct {
	RequestID       string          `json:"request_id"`
	OperationID     string          `json:"operation_id"`
	ProviderID      string          `json:"provider_id"`
	LogicalAttempt  string          `json:"logical_attempt"`
	ProviderAttempt string          `json:"provider_attempt"`
	CredentialPhase CredentialPhase `json:"credential_phase"`
}

type Response struct {
	ProtocolID             apicontract.ResponseProtocolID  `json:"protocol_id"`
	State                  ResponseState                   `json:"state"`
	MatchTiming            MatchTiming                     `json:"match_timing"`
	BoundaryReason         responseanalysis.BoundaryReason `json:"boundary_reason"`
	ElapsedMilliseconds    string                          `json:"elapsed_ms"`
	PeakProbeBytes         string                          `json:"peak_probe_bytes"`
	RawProbeBytes          string                          `json:"raw_probe_bytes"`
	DecodedProbeBytes      string                          `json:"decoded_probe_bytes"`
	UpstreamBytesRead      string                          `json:"upstream_bytes_read"`
	ClientBodyBytesWritten string                          `json:"client_body_bytes_written"`
	HeadersCommitted       bool                            `json:"headers_committed"`
	VisibleToClient        bool                            `json:"visible_to_client"`
}

type NormalizedRuleSnapshot struct {
	Name      string               `json:"name"`
	Enabled   bool                 `json:"enabled"`
	Target    errorrule.Target     `json:"target"`
	APIType   *apicontract.APIType `json:"api_type"`
	Keywords  []string             `json:"keywords"`
	MatchMode errorrule.MatchMode  `json:"match_mode"`
	Action    errorrule.Action     `json:"action"`
	Position  int64                `json:"position"`
}

type Rule struct {
	Revision              string                    `json:"revision"`
	WinnerID              errorrule.RuleID          `json:"winner_id"`
	NormalizedSnapshot    NormalizedRuleSnapshot    `json:"normalized_snapshot"`
	MatchingRuleIDs       []errorrule.RuleID        `json:"matching_rule_ids"`
	MatchedKeywords       []string                  `json:"matched_keywords"`
	MatchedKeywordIndexes []int                     `json:"matched_keyword_indexes"`
	MatchedFields         []errorrule.SemanticField `json:"matched_fields"`
}

type Retry struct {
	Action                  errorrule.ActionType `json:"action"`
	GlobalAttemptsStarted   string               `json:"global_attempts_started"`
	GlobalAttemptsRemaining *string              `json:"global_attempts_remaining"`
	GlobalAttemptsUnlimited bool                 `json:"global_attempts_unlimited"`
	RuleRetriesScheduled    string               `json:"rule_retries_scheduled"`
	RuleRetryLimit          int                  `json:"rule_retry_limit"`
}

type Alternate struct {
	Outcome      AlternateOutcome        `json:"outcome"`
	ProviderID   *string                 `json:"provider_id"`
	SwitchMode   *SwitchMode             `json:"switch_mode"`
	SwitchReason *errorrule.SwitchReason `json:"switch_reason"`
}

type Decision struct {
	Value  errorrule.DecisionValue  `json:"value"`
	Reason errorrule.DecisionReason `json:"reason"`
}

type Health struct {
	Verdict       errorrule.HealthVerdict `json:"verdict"`
	Cause         errorrule.HealthCause   `json:"cause"`
	CircuitOpened bool                    `json:"circuit_opened"`
}
