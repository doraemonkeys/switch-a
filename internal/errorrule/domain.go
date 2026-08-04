// Package errorrule owns internal-error rule validation, matching, retry
// accounting, decisions, and health classification.
package errorrule

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/google/uuid"
)

const (
	MaxRuleCount            = 256
	MaxRuleNameBytes        = 128
	MaxKeywordsPerRule      = 16
	MaxKeywordBytes         = 128
	MaxKeywordBytesPerRule  = 2_048
	MaxRuleRetries          = 10
	MaxCompiledMatcherBytes = 8 << 20
)

type ProviderID string
type RuleID string
type Revision int64

func (r Revision) Validate() error {
	if r < 0 {
		return fmt.Errorf("rule set revision must be non-negative")
	}
	return nil
}

func (r Revision) String() string {
	return fmt.Sprintf("%d", r)
}

// RuleGeneration prevents late statistics from a deleted rule from attaching
// to a later rule that happens to reuse the same public ID.
type RuleGeneration struct {
	value string
}

func ParseRuleGeneration(raw string) (RuleGeneration, error) {
	if err := validateUUIDv4(raw, "rule generation"); err != nil {
		return RuleGeneration{}, err
	}
	return RuleGeneration{value: raw}, nil
}

func (g RuleGeneration) String() string {
	return g.value
}

func (g RuleGeneration) IsZero() bool {
	return g.value == ""
}

type IDGenerator interface {
	NewID() string
}

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string {
	return uuid.NewString()
}

func GenerateRuleIdentity(generator IDGenerator) (RuleID, RuleGeneration, error) {
	if generator == nil {
		return "", RuleGeneration{}, fmt.Errorf("ID generator is required")
	}

	id := RuleID(generator.NewID())
	if err := id.Validate(); err != nil {
		return "", RuleGeneration{}, fmt.Errorf("generate rule ID: %w", err)
	}
	generation, err := ParseRuleGeneration(generator.NewID())
	if err != nil {
		return "", RuleGeneration{}, fmt.Errorf("generate rule identity: %w", err)
	}
	return id, generation, nil
}

func (id RuleID) Validate() error {
	return validateUUIDv4(string(id), "rule ID")
}

func validateUUIDv4(raw, label string) error {
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed.Version() != 4 || raw != parsed.String() {
		return fmt.Errorf("%s must be a lowercase canonical UUIDv4", label)
	}
	return nil
}

type TargetKind string

const (
	TargetGlobal   TargetKind = "global"
	TargetProvider TargetKind = "provider"
)

// Target is a closed value union. Its fields stay private so a global target
// cannot accidentally retain a provider ID after an edit changes its kind.
type Target struct {
	kind       TargetKind
	providerID ProviderID
}

func NewGlobalTarget() Target {
	return Target{kind: TargetGlobal}
}

func NewProviderTarget(providerID ProviderID) (Target, error) {
	target := Target{kind: TargetProvider, providerID: providerID}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (t Target) Kind() TargetKind {
	return t.kind
}

func (t Target) ProviderID() (ProviderID, bool) {
	return t.providerID, t.kind == TargetProvider
}

func (t Target) Validate() error {
	switch t.kind {
	case TargetGlobal:
		if t.providerID != "" {
			return fmt.Errorf("global target cannot have provider_id")
		}
	case TargetProvider:
		if err := validateProviderID(t.providerID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown target kind %q", t.kind)
	}
	return nil
}

func validateProviderID(providerID ProviderID) error {
	raw := string(providerID)
	if raw == "" {
		return fmt.Errorf("provider target requires provider_id")
	}
	if strings.TrimSpace(raw) != raw {
		return fmt.Errorf("provider_id cannot have surrounding whitespace")
	}
	return nil
}

type ActionType string

const (
	ActionPassthrough     ActionType = "passthrough"
	ActionRetryOnly       ActionType = "retry_only"
	ActionRetryThenSwitch ActionType = "retry_then_switch"
)

type RetryPolicy struct {
	MaxRetries int
	Backoff    model.BackoffPolicy
}

// Action is a closed value union. Retry data cannot exist on passthrough and is
// available only through RetryPolicy after checking the discriminator.
type Action struct {
	actionType ActionType
	retry      RetryPolicy
}

func NewPassthroughAction() Action {
	return Action{actionType: ActionPassthrough}
}

func NewRetryOnlyAction(maxRetries int, backoff model.BackoffPolicy) (Action, error) {
	return newRetryAction(ActionRetryOnly, maxRetries, backoff)
}

func NewRetryThenSwitchAction(maxRetries int, backoff model.BackoffPolicy) (Action, error) {
	return newRetryAction(ActionRetryThenSwitch, maxRetries, backoff)
}

func newRetryAction(actionType ActionType, maxRetries int, backoff model.BackoffPolicy) (Action, error) {
	action := Action{actionType: actionType, retry: RetryPolicy{MaxRetries: maxRetries, Backoff: backoff}}
	if err := action.Validate(); err != nil {
		return Action{}, err
	}
	return action, nil
}

func (a Action) Type() ActionType {
	return a.actionType
}

func (a Action) RetryPolicy() (RetryPolicy, bool) {
	return a.retry, a.actionType == ActionRetryOnly || a.actionType == ActionRetryThenSwitch
}

func (a Action) Validate() error {
	switch a.actionType {
	case ActionPassthrough:
		if a.retry != (RetryPolicy{}) {
			return fmt.Errorf("passthrough action cannot contain retry fields")
		}
		return nil
	case ActionRetryOnly, ActionRetryThenSwitch:
		return ValidateRetryPolicy(a.retry.MaxRetries, a.retry.Backoff)
	default:
		return fmt.Errorf("unknown action type %q", a.actionType)
	}
}

func ValidateRetryPolicy(maxRetries int, backoff model.BackoffPolicy) error {
	if maxRetries < 0 || maxRetries > MaxRuleRetries {
		return fmt.Errorf("max_retries must be between 0 and %d", MaxRuleRetries)
	}
	if math.IsNaN(backoff.Multiplier) || math.IsInf(backoff.Multiplier, 0) {
		return fmt.Errorf("multiplier must be finite")
	}
	return backoff.Validate()
}

type MatchMode string

const (
	MatchAny MatchMode = "any"
	MatchAll MatchMode = "all"
)

type RuleSpec struct {
	Name      string               `json:"name"`
	Enabled   bool                 `json:"enabled"`
	Target    Target               `json:"target"`
	APIType   *apicontract.APIType `json:"api_type"`
	Keywords  []string             `json:"keywords"`
	MatchMode MatchMode            `json:"match_mode"`
	Action    Action               `json:"action"`
}

type Rule struct {
	ID RuleID `json:"id"`
	RuleSpec
	Position   int64     `json:"position"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	generation RuleGeneration
}

type RuleMetadata struct {
	ID         RuleID
	Generation RuleGeneration
	Position   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func NewRule(spec RuleSpec, metadata RuleMetadata) Rule {
	return Rule{
		ID:         metadata.ID,
		RuleSpec:   cloneRuleSpec(spec),
		Position:   metadata.Position,
		CreatedAt:  metadata.CreatedAt,
		UpdatedAt:  metadata.UpdatedAt,
		generation: metadata.Generation,
	}
}

func (r Rule) Generation() RuleGeneration {
	return r.generation
}

type RuleStats struct {
	RuleID    RuleID     `json:"rule_id"`
	HitCount  uint64     `json:"hit_count"`
	LastHitAt *time.Time `json:"last_hit_at"`
}

func cloneRuleSpec(spec RuleSpec) RuleSpec {
	clone := spec
	clone.Keywords = append([]string(nil), spec.Keywords...)
	if spec.APIType != nil {
		apiType := *spec.APIType
		clone.APIType = &apiType
	}
	return clone
}

func cloneRule(rule Rule) Rule {
	clone := rule
	clone.RuleSpec = cloneRuleSpec(rule.RuleSpec)
	return clone
}
