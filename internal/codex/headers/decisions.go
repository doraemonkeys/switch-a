package codexheaders

import "net/http"

// ClientInput combines request Header evidence with an optional inspected
// response.create message. A zero MessageView means that no body/frame was
// supplied, which is distinct from inspecting an empty malformed payload.
type ClientInput struct {
	Headers         http.Header
	Message         MessageView
	Owners          OwnerLookup
	AttestationLock OperationLockStatus
}

// DecideClient validates carrier consistency before consulting ownership. This
// ordering ensures malformed or contradictory wire input cannot produce an
// actionable claim.
func DecideClient(input ClientInput) Result {
	result := Result{wire: input.Message.wire}
	if rejection, rejected := rejectInvalidClientMessage(input.Message); rejected {
		return rejection
	}

	fields := collectClientIdentityFields(&result, input)
	fields = append(fields, collectClientContinuityFields(&result, input)...)
	attestation := observeHeader(input.Headers, attestationHeader)
	if attestation.present && !attestation.valid {
		result.decisions = append(result.decisions, newDecision(
			ActionReject, FieldAttestation, CarrierHeader, ReasonMalformedHeader,
			BindingCandidate{}, attestation.names, ClaimSpec{},
		))
	}
	if result.Rejected() {
		return result
	}

	resolveClientFields(&result, fields, input.Owners)
	if attestation.present {
		result.decisions = append(result.decisions, decideAttestation(attestation, input.AttestationLock))
	}
	return result
}

type resolvedField struct {
	field       Field
	carriers    Carrier
	candidate   BindingCandidate
	headerNames []string
	policy      ownerPolicy
}

func rejectInvalidClientMessage(message MessageView) (Result, bool) {
	if !message.present {
		return Result{}, false
	}
	if message.direction != directionClient {
		return rejectResult(message.wire, FieldEnvelope, CarrierEnvelope, ReasonInvalidEnvelope), true
	}
	if message.issue != nil {
		return rejectResult(message.wire, message.issue.field, CarrierEnvelope, message.issue.reason), true
	}
	return Result{}, false
}

func collectClientIdentityFields(result *Result, input ClientInput) []resolvedField {
	var fields []resolvedField
	for _, spec := range identityHeaderSpecs {
		header := observeHeader(input.Headers, spec)
		projection, projected := input.Message.values[spec.field]
		if !header.present && !projected {
			continue
		}
		if header.present && !header.valid {
			if projected {
				result.decisions = append(result.decisions, newDecision(
					ActionReject, spec.field, CarrierHeader|CarrierProjection,
					ReasonMalformedHeader, BindingCandidate{}, header.names, ClaimSpec{},
				))
			} else {
				result.decisions = append(result.decisions, newDecision(
					ActionDrop, spec.field, CarrierHeader, ReasonMalformedHeader,
					BindingCandidate{}, header.names, ClaimSpec{},
				))
			}
			continue
		}
		if header.present && projected && !header.value.Equal(projection) {
			result.decisions = append(result.decisions, newDecision(
				ActionReject, spec.field, CarrierHeader|CarrierProjection,
				ReasonCarrierConflict, BindingCandidate{}, header.names, ClaimSpec{},
			))
			continue
		}
		value, carriers := mergeCarriers(header, projection, projected)
		fields = append(fields, resolvedField{
			field:       spec.field,
			carriers:    carriers,
			candidate:   BindingCandidate{field: spec.field, value: value},
			headerNames: header.names,
			policy:      policyIdentity,
		})
	}
	return fields
}

func collectClientContinuityFields(result *Result, input ClientInput) []resolvedField {
	var fields []resolvedField
	for _, state := range []struct {
		spec       headerSpec
		projection Field
		policy     ownerPolicy
	}{
		{spec: turnStateHeader, projection: FieldTurnState, policy: policyExistingOnly},
		{spec: turnMetadataHeader, projection: FieldTurnMetadata, policy: policyClaimable},
	} {
		header := observeHeader(input.Headers, state.spec)
		projection, projected := input.Message.values[state.projection]
		if !header.present && !projected {
			continue
		}
		if header.present && !header.valid {
			result.decisions = append(result.decisions, newDecision(
				ActionReject, state.spec.field, CarrierHeader, ReasonMalformedHeader,
				BindingCandidate{}, header.names, ClaimSpec{},
			))
			continue
		}
		if header.present && projected && !header.value.Equal(projection) {
			result.decisions = append(result.decisions, newDecision(
				ActionReject, state.spec.field, CarrierHeader|CarrierProjection,
				ReasonCarrierConflict, BindingCandidate{}, header.names, ClaimSpec{},
			))
			continue
		}
		value, carriers := mergeCarriers(header, projection, projected)
		fields = append(fields, resolvedField{
			field:       state.spec.field,
			carriers:    carriers,
			candidate:   BindingCandidate{field: state.spec.field, value: value},
			headerNames: header.names,
			policy:      state.policy,
		})
	}

	if previous, present := input.Message.values[FieldResponseReference]; present {
		fields = append(fields, resolvedField{
			field:     FieldResponseReference,
			carriers:  CarrierProjection,
			candidate: BindingCandidate{field: FieldResponseReference, value: previous},
			policy:    policyExistingOnly,
		})
	}
	return fields
}

func resolveClientFields(result *Result, fields []resolvedField, owners OwnerLookup) {
	for _, field := range fields {
		status := OwnerUnavailable
		if owners != nil {
			status = owners(field.candidate)
		}
		result.decisions = append(result.decisions, decideOwner(
			field.candidate, field.carriers, field.headerNames, status, field.policy,
		))
	}
}

// DecideServerHeaders enforces the asymmetric response contract: only Turn
// State may be bound and forwarded. Metadata and attestation are never echoed.
func DecideServerHeaders(headers http.Header, owners OwnerLookup) Result {
	var result Result
	for _, spec := range []headerSpec{turnMetadataHeader, attestationHeader} {
		observation := observeHeader(headers, spec)
		if !observation.present {
			continue
		}
		result.decisions = append(result.decisions, newDecision(
			ActionDrop, spec.field, CarrierHeader, ReasonResponseEchoForbidden,
			BindingCandidate{}, observation.names, ClaimSpec{},
		))
	}
	state := observeHeader(headers, turnStateHeader)
	if !state.present {
		return result
	}
	if !state.valid {
		result.decisions = append(result.decisions, newDecision(
			ActionReject, FieldTurnState, CarrierHeader, ReasonMalformedHeader,
			BindingCandidate{}, state.names, ClaimSpec{},
		))
		return result
	}
	candidate := BindingCandidate{field: FieldTurnState, value: state.value}
	status := OwnerUnavailable
	if owners != nil {
		status = owners(candidate)
	}
	result.decisions = append(result.decisions, decideOwner(
		candidate, CarrierHeader, state.names, status, policyResponseClaimable,
	))
	return result
}

// DecideServerMessage validates only target-fixture server paths. New opaque
// values request pending response claims; persistence remains transport-owned.
func DecideServerMessage(message MessageView, owners OwnerLookup) Result {
	if !message.present || message.direction != directionServer {
		return rejectResult(message.wire, FieldEnvelope, CarrierEnvelope, ReasonInvalidEnvelope)
	}
	if message.issue != nil {
		return rejectResult(message.wire, message.issue.field, CarrierEnvelope, message.issue.reason)
	}
	result := Result{wire: message.wire}
	for _, field := range []Field{FieldTurnState, FieldResponseReference} {
		value, present := message.values[field]
		if !present {
			continue
		}
		candidate := BindingCandidate{field: field, value: value}
		status := OwnerUnavailable
		if owners != nil {
			status = owners(candidate)
		}
		result.decisions = append(result.decisions, decideOwner(
			candidate, CarrierFrame, nil, status, policyResponseClaimable,
		))
	}
	return result
}

type ownerPolicy uint8

const (
	policyIdentity ownerPolicy = iota + 1
	policyClaimable
	policyExistingOnly
	policyResponseClaimable
)

func decideOwner(
	candidate BindingCandidate,
	carriers Carrier,
	headerNames []string,
	status OwnerStatus,
	policy ownerPolicy,
) Decision {
	switch status {
	case OwnerCurrent:
		return newDecision(ActionForward, candidate.field, carriers, ReasonOwnerMatch, candidate, headerNames, ClaimSpec{})
	case OwnerUnknown:
		if policy == policyIdentity || policy == policyClaimable || policy == policyResponseClaimable {
			return newDecision(
				ActionClaim,
				candidate.field,
				carriers,
				ReasonOwnerUnknown,
				candidate,
				headerNames,
				ClaimSpec{lifetime: ClaimLifetimeDurable, boundary: ClaimBoundaryProtocolScope},
			)
		}
		return newDecision(ActionReject, candidate.field, carriers, ReasonOwnerUnknown, candidate, headerNames, ClaimSpec{})
	case OwnerConflict:
		if policy == policyIdentity && carriers == CarrierHeader {
			return newDecision(ActionDrop, candidate.field, carriers, ReasonOwnerConflict, candidate, headerNames, ClaimSpec{})
		}
		return newDecision(ActionReject, candidate.field, carriers, ReasonOwnerConflict, candidate, headerNames, ClaimSpec{})
	default:
		return newDecision(ActionReject, candidate.field, carriers, ReasonOwnerUnavailable, candidate, headerNames, ClaimSpec{})
	}
}

func decideAttestation(observation headerObservation, lock OperationLockStatus) Decision {
	candidate := BindingCandidate{field: FieldAttestation, value: observation.value}
	switch lock {
	case OperationUnlocked:
		return newDecision(
			ActionClaim,
			FieldAttestation,
			CarrierHeader,
			ReasonOperationUnlocked,
			candidate,
			observation.names,
			ClaimSpec{lifetime: ClaimLifetimeOperation, boundary: ClaimBoundaryAuthority},
		)
	case OperationAuthorityCurrent:
		return newDecision(ActionForward, FieldAttestation, CarrierHeader, ReasonOperationMatch, candidate, observation.names, ClaimSpec{})
	case OperationAuthorityConflict:
		return newDecision(ActionReject, FieldAttestation, CarrierHeader, ReasonOperationConflict, candidate, observation.names, ClaimSpec{})
	default:
		return newDecision(ActionReject, FieldAttestation, CarrierHeader, ReasonOperationUnavailable, candidate, observation.names, ClaimSpec{})
	}
}

func mergeCarriers(header headerObservation, projection OpaqueValue, projected bool) (OpaqueValue, Carrier) {
	if header.present {
		carriers := CarrierHeader
		if projected {
			carriers |= CarrierProjection
		}
		return header.value, carriers
	}
	return projection, CarrierProjection
}

func newDecision(
	action Action,
	field Field,
	carriers Carrier,
	reason Reason,
	candidate BindingCandidate,
	headerNames []string,
	claim ClaimSpec,
) Decision {
	return Decision{
		action:      action,
		field:       field,
		carriers:    carriers,
		reason:      reason,
		candidate:   candidate,
		headerNames: append([]string(nil), headerNames...),
		claim:       claim,
	}
}

func rejectResult(wire []byte, field Field, carriers Carrier, reason Reason) Result {
	return Result{
		wire: wire,
		decisions: []Decision{newDecision(
			ActionReject, field, carriers, reason, BindingCandidate{}, nil, ClaimSpec{},
		)},
	}
}
