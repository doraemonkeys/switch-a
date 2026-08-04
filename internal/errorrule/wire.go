package errorrule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func (s RuleStats) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RuleID    RuleID     `json:"rule_id"`
		HitCount  string     `json:"hit_count"`
		LastHitAt *time.Time `json:"last_hit_at"`
	}{RuleID: s.RuleID, HitCount: strconv.FormatUint(s.HitCount, 10), LastHitAt: s.LastHitAt})
}

func (s *RuleStats) UnmarshalJSON(data []byte) error {
	var wire struct {
		RuleID    RuleID     `json:"rule_id"`
		HitCount  string     `json:"hit_count"`
		LastHitAt *time.Time `json:"last_hit_at"`
	}
	if err := decodeStrict(data, &wire); err != nil {
		return fmt.Errorf("decode rule stats: %w", err)
	}
	if err := wire.RuleID.Validate(); err != nil {
		return err
	}
	hitCount, err := strconv.ParseUint(wire.HitCount, 10, 64)
	if err != nil || wire.HitCount != strconv.FormatUint(hitCount, 10) {
		return fmt.Errorf("hit_count must be a canonical unsigned decimal string")
	}
	if wire.LastHitAt != nil {
		_, offset := wire.LastHitAt.Zone()
		if offset != 0 {
			return fmt.Errorf("last_hit_at must be UTC")
		}
	}
	*s = RuleStats{RuleID: wire.RuleID, HitCount: hitCount, LastHitAt: wire.LastHitAt}
	return nil
}

func (t Target) MarshalJSON() ([]byte, error) {
	switch t.kind {
	case TargetGlobal:
		return json.Marshal(struct {
			Kind TargetKind `json:"kind"`
		}{Kind: TargetGlobal})
	case TargetProvider:
		if err := t.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Kind       TargetKind `json:"kind"`
			ProviderID ProviderID `json:"provider_id"`
		}{Kind: TargetProvider, ProviderID: t.providerID})
	default:
		return nil, fmt.Errorf("cannot marshal unknown target kind %q", t.kind)
	}
}

func (t *Target) UnmarshalJSON(data []byte) error {
	var wire struct {
		Kind       TargetKind  `json:"kind"`
		ProviderID *ProviderID `json:"provider_id"`
	}
	if err := decodeStrict(data, &wire); err != nil {
		return fmt.Errorf("decode target: %w", err)
	}

	switch wire.Kind {
	case TargetGlobal:
		if wire.ProviderID != nil {
			return fmt.Errorf("global target cannot have provider_id")
		}
		*t = NewGlobalTarget()
		return nil
	case TargetProvider:
		if wire.ProviderID == nil {
			return fmt.Errorf("provider target requires provider_id")
		}
		target, err := NewProviderTarget(*wire.ProviderID)
		if err != nil {
			return err
		}
		*t = target
		return nil
	default:
		return fmt.Errorf("unknown target kind %q", wire.Kind)
	}
}

func (a Action) MarshalJSON() ([]byte, error) {
	switch a.actionType {
	case ActionPassthrough:
		if err := a.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Type ActionType `json:"type"`
		}{Type: ActionPassthrough})
	case ActionRetryOnly, ActionRetryThenSwitch:
		if err := a.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Type       ActionType          `json:"type"`
			MaxRetries int                 `json:"max_retries"`
			Backoff    model.BackoffPolicy `json:"backoff"`
		}{Type: a.actionType, MaxRetries: a.retry.MaxRetries, Backoff: a.retry.Backoff})
	default:
		return nil, fmt.Errorf("cannot marshal unknown action type %q", a.actionType)
	}
}

func (a *Action) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type       ActionType      `json:"type"`
		MaxRetries *int            `json:"max_retries"`
		Backoff    json.RawMessage `json:"backoff"`
	}
	if err := decodeStrict(data, &wire); err != nil {
		return fmt.Errorf("decode action: %w", err)
	}

	switch wire.Type {
	case ActionPassthrough:
		if wire.MaxRetries != nil || len(wire.Backoff) != 0 {
			return fmt.Errorf("passthrough action cannot contain retry fields")
		}
		*a = NewPassthroughAction()
		return nil
	case ActionRetryOnly, ActionRetryThenSwitch:
		if wire.MaxRetries == nil || len(wire.Backoff) == 0 {
			return fmt.Errorf("%s action requires max_retries and backoff", wire.Type)
		}
		var backoff model.BackoffPolicy
		if err := decodeStrict(wire.Backoff, &backoff); err != nil {
			return fmt.Errorf("decode backoff: %w", err)
		}
		action, err := newRetryAction(wire.Type, *wire.MaxRetries, backoff)
		if err != nil {
			return err
		}
		*a = action
		return nil
	default:
		return fmt.Errorf("unknown action type %q", wire.Type)
	}
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
