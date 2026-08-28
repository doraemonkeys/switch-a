// Package codexheaders recognizes the stable Codex wire fields owned by the
// gateway and turns them into typed policy decisions. It never owns transport,
// persistence, or mutable request state.
package codexheaders

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

const bindingInputCodec = "codex-header-binding/v1"

// Field is a normalized security-field category. Session aliases deliberately
// collapse to one category while the other identity fields remain distinct.
type Field string

const (
	FieldEnvelope          Field = "envelope"
	FieldThreadID          Field = "thread_id"
	FieldSessionID         Field = "session_id"
	FieldConversationID    Field = "conversation_id"
	FieldWindowID          Field = "window_id"
	FieldTurnState         Field = "turn_state"
	FieldTurnMetadata      Field = "turn_metadata"
	FieldAttestation       Field = "attestation"
	FieldResponseReference Field = "response_reference"
)

// Carrier identifies where an opaque value was observed. It is a bit mask
// because one decision can cover matching Header and projection values.
type Carrier uint8

const (
	CarrierNone Carrier = 0
)

const (
	CarrierHeader Carrier = 1 << iota
	CarrierProjection
	CarrierFrame
	CarrierEnvelope
)

func (c Carrier) Has(want Carrier) bool { return c&want != 0 }

// Action is the complete vocabulary understood by transport integrations.
// Claim establishes new response-owned state, while Adopt binds existing state
// only after the transport independently anchors its ProtocolScope. Neither
// action means the codexheaders package mutated persistence itself.
type Action string

const (
	ActionForward         Action = "forward"
	ActionForwardDegraded Action = "forward_degraded"
	ActionDrop            Action = "drop"
	ActionReject          Action = "reject"
	ActionClaim           Action = "claim"
	ActionAdopt           Action = "adopt"
)

// Reason is stable, non-secret decision context suitable for structured logs.
type Reason string

const (
	ReasonNone                  Reason = "none"
	ReasonOwnerMatch            Reason = "owner_match"
	ReasonOwnerUnknown          Reason = "owner_unknown"
	ReasonOwnerConflict         Reason = "owner_conflict"
	ReasonOwnerUnavailable      Reason = "owner_unavailable"
	ReasonMalformedHeader       Reason = "malformed_header"
	ReasonCarrierConflict       Reason = "carrier_conflict"
	ReasonInvalidEnvelope       Reason = "invalid_envelope"
	ReasonInvalidProjection     Reason = "invalid_projection"
	ReasonDuplicateSecurityKey  Reason = "duplicate_security_key"
	ReasonOperationUnlocked     Reason = "operation_unlocked"
	ReasonOperationMatch        Reason = "operation_match"
	ReasonOperationConflict     Reason = "operation_conflict"
	ReasonOperationUnavailable  Reason = "operation_unavailable"
	ReasonResponseEchoForbidden Reason = "response_echo_forbidden"
)

// ResponseLifecycle is the gateway-owned lifecycle effect of a recognized
// server event. Unknown and future events remain ResponseLifecycleNone.
type ResponseLifecycle uint8

const (
	ResponseLifecycleNone ResponseLifecycle = iota
	ResponseLifecycleActive
	ResponseLifecycleTerminal
)

// OwnerStatus is supplied by the continuity layer after looking up a binding
// against the current ClientScope and ProtocolScope. Zero is deliberately
// unavailable so an omitted lookup cannot accidentally authorize a claim.
type OwnerStatus uint8

const (
	OwnerUnavailable OwnerStatus = iota
	OwnerStoreUnavailable
	OwnerUnknown
	OwnerCurrent
	OwnerConflict
)

// OwnerLookup is a read-only capability. Atomic claim remains the caller's
// responsibility and is requested through an ActionClaim decision.
type OwnerLookup func(BindingCandidate) OwnerStatus

// StateAdmission describes whether the transport already has an independent,
// unambiguous ProtocolScope anchor. Existing state may be adopted only inside
// that scope; ordinary provider selection is not ownership evidence.
type StateAdmission uint8

const (
	StateAdmissionStrict StateAdmission = iota
	StateAdmissionAnchored
)

// OperationLockStatus describes whether the current logical operation is
// already constrained to the candidate Authority. Attestation never enters the
// durable owner lookup.
type OperationLockStatus uint8

const (
	OperationLockUnavailable OperationLockStatus = iota
	OperationUnlocked
	OperationAuthorityCurrent
	OperationAuthorityConflict
)

type ClaimLifetime string

const (
	ClaimLifetimeDurable   ClaimLifetime = "durable"
	ClaimLifetimeOperation ClaimLifetime = "operation"
)

type ClaimBoundary string

const (
	ClaimBoundaryProtocolScope ClaimBoundary = "protocol_scope"
	ClaimBoundaryAuthority     ClaimBoundary = "authority"
)

// ClaimSpec prevents an operation-local attestation lock from being mistaken
// for a durable continuity binding.
type ClaimSpec struct {
	lifetime ClaimLifetime
	boundary ClaimBoundary
}

func (c ClaimSpec) Lifetime() ClaimLifetime { return c.lifetime }
func (c ClaimSpec) Boundary() ClaimBoundary { return c.boundary }

// OpaqueValue intentionally formats as redacted. Bytes returns a copy for HMAC
// input; callers should not place it in logs or errors.
type OpaqueValue struct {
	value string
}

func newOpaqueValue(value string) OpaqueValue { return OpaqueValue{value: value} }

func (v OpaqueValue) Bytes() []byte { return []byte(v.value) }
func (v OpaqueValue) Empty() bool   { return v.value == "" }
func (v OpaqueValue) Equal(other OpaqueValue) bool {
	return v.value == other.value
}
func (OpaqueValue) String() string     { return "opaque-value(redacted)" }
func (v OpaqueValue) GoString() string { return v.String() }

// BindingCandidate supplies both the broad codexidentity namespace and a
// category-prefixed digest input. Prefixing is required so equal opaque bytes
// in Thread-Id and Session-Id can never address the same durable record.
type BindingCandidate struct {
	field Field
	value OpaqueValue
}

func (c BindingCandidate) Field() Field       { return c.field }
func (c BindingCandidate) Value() OpaqueValue { return c.value }

func (c BindingCandidate) PersistentNamespace() (codexidentity.OpaqueNamespace, bool) {
	switch c.field {
	case FieldThreadID, FieldSessionID, FieldConversationID, FieldWindowID:
		return codexidentity.OpaqueSessionIdentity, true
	case FieldTurnState:
		return codexidentity.OpaqueTurnState, true
	case FieldTurnMetadata:
		return codexidentity.OpaqueTurnMetadata, true
	case FieldResponseReference:
		return codexidentity.OpaqueResponseReference, true
	default:
		return "", false
	}
}

func (c BindingCandidate) DigestInput() []byte {
	if _, persistent := c.PersistentNamespace(); !persistent || c.value.Empty() {
		return nil
	}
	return encodeBindingFields([]byte(bindingInputCodec), []byte(c.field), []byte(c.value.value))
}

func (c BindingCandidate) String() string {
	return fmt.Sprintf("binding-candidate(field=%s,value=redacted)", c.field)
}

func (c BindingCandidate) GoString() string { return c.String() }

func encodeBindingFields(fields ...[]byte) []byte {
	total := 0
	for _, field := range fields {
		total += 8 + len(field)
	}
	encoded := make([]byte, 0, total)
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		encoded = append(encoded, size[:]...)
		encoded = append(encoded, field...)
	}
	return encoded
}

// Decision is immutable. HeaderNames contains every case/alias spelling that
// must be removed together for a drop decision.
type Decision struct {
	action      Action
	field       Field
	carriers    Carrier
	reason      Reason
	candidate   BindingCandidate
	headerNames []string
	claim       ClaimSpec
}

func (d Decision) Action() Action              { return d.action }
func (d Decision) Field() Field                { return d.field }
func (d Decision) Carriers() Carrier           { return d.carriers }
func (d Decision) Reason() Reason              { return d.reason }
func (d Decision) Candidate() BindingCandidate { return d.candidate }
func (d Decision) Claim() ClaimSpec            { return d.claim }
func (d Decision) HeaderNames() []string       { return append([]string(nil), d.headerNames...) }

// Result keeps the original wire buffer solely for replay. Claims and drops
// are actionable only when Rejected is false.
type Result struct {
	decisions []Decision
	wire      []byte
}

func (r Result) Decisions() []Decision { return append([]Decision(nil), r.decisions...) }

func (r Result) Outcome() Action {
	outcome := ActionForward
	for _, decision := range r.decisions {
		switch decision.action {
		case ActionReject:
			return ActionReject
		case ActionAdopt:
			outcome = ActionAdopt
		case ActionClaim:
			if outcome != ActionAdopt {
				outcome = ActionClaim
			}
		case ActionForwardDegraded:
			if outcome == ActionForward || outcome == ActionDrop {
				outcome = ActionForwardDegraded
			}
		case ActionDrop:
			if outcome == ActionForward {
				outcome = ActionDrop
			}
		}
	}
	return outcome
}

func (r Result) Rejected() bool { return r.Outcome() == ActionReject }

// ReplayBytes returns the exact caller-owned buffer. The package never mutates
// or replaces it; transport code can therefore replay compressed or framed
// bytes without a JSON round trip.
func (r Result) ReplayBytes() []byte { return r.wire }

func (r Result) Claims() []Decision {
	if r.Rejected() {
		return nil
	}
	return decisionsByAction(r.decisions, ActionClaim)
}

func (r Result) Adoptions() []Decision {
	if r.Rejected() {
		return nil
	}
	return decisionsByAction(r.decisions, ActionAdopt)
}

func (r Result) HeaderNamesToDrop() []string {
	if r.Rejected() {
		return nil
	}
	seen := make(map[string]struct{})
	var names []string
	for _, decision := range r.decisions {
		if decision.action != ActionDrop {
			continue
		}
		for _, name := range decision.headerNames {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func decisionsByAction(decisions []Decision, action Action) []Decision {
	result := make([]Decision, 0, len(decisions))
	for _, decision := range decisions {
		if decision.action == action {
			result = append(result, decision)
		}
	}
	return result
}

type headerSpec struct {
	field   Field
	aliases []string
}

var identityHeaderSpecs = []headerSpec{
	{field: FieldThreadID, aliases: []string{"Thread-Id"}},
	{field: FieldSessionID, aliases: []string{"Session-Id", "session_id"}},
	{field: FieldConversationID, aliases: []string{"Conversation_id"}},
	{field: FieldWindowID, aliases: []string{"X-Codex-Window-Id"}},
}

var (
	turnStateHeader    = headerSpec{field: FieldTurnState, aliases: []string{"X-Codex-Turn-State"}}
	turnMetadataHeader = headerSpec{field: FieldTurnMetadata, aliases: []string{"X-Codex-Turn-Metadata"}}
	attestationHeader  = headerSpec{field: FieldAttestation, aliases: []string{"X-Oai-Attestation"}}
)

type headerObservation struct {
	present bool
	valid   bool
	value   OpaqueValue
	names   []string
}

func observeHeader(headers http.Header, spec headerSpec) headerObservation {
	observation := headerObservation{valid: true}
	for name, values := range headers {
		if !matchesAlias(name, spec.aliases) {
			continue
		}
		observation.present = true
		observation.names = append(observation.names, name)
		if len(values) == 0 {
			observation.valid = false
			continue
		}
		for _, value := range values {
			if value == "" {
				observation.valid = false
				continue
			}
			candidate := newOpaqueValue(value)
			if observation.value.Empty() {
				observation.value = candidate
				continue
			}
			if !observation.value.Equal(candidate) {
				observation.valid = false
			}
		}
	}
	sort.Strings(observation.names)
	if observation.present && observation.value.Empty() {
		observation.valid = false
	}
	return observation
}

func matchesAlias(name string, aliases []string) bool {
	for _, alias := range aliases {
		if strings.EqualFold(name, alias) {
			return true
		}
	}
	return false
}
