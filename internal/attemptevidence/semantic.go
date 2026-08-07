package attemptevidence

import (
	"fmt"
	"strconv"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

// NewSemanticError preserves semantic diagnostics and replaces only the
// explicitly supplied switch-a credential. An empty value intentionally means
// that no switch-a-owned credential was injected for this attempt.
func NewSemanticError(facts Facts, injectedCredential string) (SemanticError, error) {
	if err := validateFacts(facts); err != nil {
		return SemanticError{}, err
	}

	winner := facts.Rule.Winner
	matchingRuleIDs := make([]errorrule.RuleID, len(facts.Rule.Matches))
	for index, match := range facts.Rule.Matches {
		matchingRuleIDs[index] = match.Rule.ID
	}
	matchedKeywords := make([]string, len(winner.MatchedKeywords))
	for index, keyword := range winner.MatchedKeywords {
		matchedKeywords[index] = SanitizeSnippet(keyword, injectedCredential)
	}
	keywords := make([]string, len(winner.Rule.Keywords))
	for index, keyword := range winner.Rule.Keywords {
		keywords[index] = SanitizeSnippet(keyword, injectedCredential)
	}
	var remaining *string
	if !facts.Retry.GlobalAttemptsUnlimited {
		value := decimal(facts.Retry.GlobalAttemptsRemaining)
		remaining = &value
	}

	return SemanticError{
		SchemaVersion: SemanticSchemaVersion,
		Identity: Identity{
			RequestID: facts.Identity.RequestID, OperationID: facts.Identity.OperationID,
			ProviderID:      facts.Identity.ProviderID,
			LogicalAttempt:  decimal(facts.Identity.LogicalAttempt),
			ProviderAttempt: decimal(facts.Identity.ProviderAttempt),
			CredentialPhase: facts.Identity.CredentialPhase,
		},
		Response: Response{
			ProtocolID: facts.Response.ProtocolID, State: facts.Response.State,
			MatchTiming: facts.Response.MatchTiming, BoundaryReason: facts.Response.BoundaryReason,
			ElapsedMilliseconds:    decimal(facts.Response.ElapsedMilliseconds),
			PeakProbeBytes:         decimal(facts.Response.PeakProbeBytes),
			RawProbeBytes:          decimal(facts.Response.RawProbeBytes),
			DecodedProbeBytes:      decimal(facts.Response.DecodedProbeBytes),
			UpstreamBytesRead:      decimal(facts.Response.UpstreamBytesRead),
			ClientBodyBytesWritten: decimal(facts.Response.ClientBodyBytesWritten),
			HeadersCommitted:       facts.Response.HeadersCommitted,
			VisibleToClient:        facts.Response.VisibleToClient,
		},
		Rule: Rule{
			Revision: facts.Rule.Revision.String(), WinnerID: winner.Rule.ID,
			NormalizedSnapshot: NormalizedRuleSnapshot{
				Name: SanitizeSnippet(winner.Rule.Name, injectedCredential), Enabled: winner.Rule.Enabled,
				Target: winner.Rule.Target, APIType: winner.Rule.APIType,
				Keywords: keywords, MatchMode: winner.Rule.MatchMode,
				Action: winner.Rule.Action, Position: winner.Rule.Position,
			},
			MatchingRuleIDs:       matchingRuleIDs,
			MatchedKeywords:       matchedKeywords,
			MatchedKeywordIndexes: append([]int(nil), winner.MatchedKeywordIndexes...),
			MatchedFields:         append([]errorrule.SemanticField(nil), winner.MatchedFields...),
		},
		Retry: Retry{
			Action:                  winner.Rule.Action.Type(),
			GlobalAttemptsStarted:   decimal(facts.Retry.GlobalAttemptsStarted),
			GlobalAttemptsRemaining: remaining,
			GlobalAttemptsUnlimited: facts.Retry.GlobalAttemptsUnlimited,
			RuleRetriesScheduled:    decimal(facts.Retry.RuleRetriesScheduled),
			RuleRetryLimit:          facts.Retry.RuleRetryLimit,
		},
		Alternate: Alternate{
			Outcome: facts.Alternate.Outcome, ProviderID: cloneString(facts.Alternate.ProviderID),
			SwitchMode:   cloneSwitchMode(facts.Alternate.SwitchMode),
			SwitchReason: cloneSwitchReason(facts.Alternate.SwitchReason),
		},
		Decision: Decision{Value: facts.Decision.Value, Reason: facts.Decision.Reason},
		Health: Health{
			Verdict: facts.Health.Assessment.Verdict, Cause: facts.Health.Assessment.Cause,
			CircuitOpened: facts.Health.CircuitOpened,
		},
	}, nil
}

func validateFacts(facts Facts) error {
	identity := facts.Identity
	for label, value := range map[string]string{
		"request_id": identity.RequestID, "operation_id": identity.OperationID, "provider_id": identity.ProviderID,
	} {
		if value == "" || len(value) > MaxIdentityBytes {
			return fmt.Errorf("%s must contain between 1 and %d bytes", label, MaxIdentityBytes)
		}
	}
	if identity.LogicalAttempt == 0 || identity.ProviderAttempt == 0 {
		return fmt.Errorf("logical and provider attempts are one-based")
	}
	if identity.CredentialPhase != CredentialPhasePrimary && identity.CredentialPhase != CredentialPhaseRefreshed {
		return fmt.Errorf("unknown credential phase %q", identity.CredentialPhase)
	}
	if err := validateResponse(facts.Response); err != nil {
		return err
	}
	if err := validateRuleFacts(facts.Rule); err != nil {
		return err
	}
	if facts.Retry.GlobalAttemptsStarted == 0 {
		return fmt.Errorf("global attempts started must be positive")
	}
	if facts.Retry.RuleRetryLimit < 0 || facts.Retry.RuleRetryLimit > errorrule.MaxRuleRetries {
		return fmt.Errorf("rule retry limit is outside the domain bound")
	}
	expectedRuleLimit := 0
	if policy, ok := facts.Rule.Winner.Rule.Action.RetryPolicy(); ok {
		expectedRuleLimit = policy.MaxRetries
	}
	if facts.Retry.RuleRetryLimit != expectedRuleLimit || facts.Retry.RuleRetriesScheduled > uint64(expectedRuleLimit) {
		return fmt.Errorf("retry facts do not match the winning rule policy")
	}
	if err := validateDecision(facts.Decision); err != nil {
		return err
	}
	if err := validateHealth(facts.Health); err != nil {
		return err
	}
	if err := validateAlternate(facts.Alternate); err != nil {
		return err
	}
	return validateSemanticConsistency(facts)
}

func validateResponse(response ResponseFacts) error {
	if !knownProtocolID(response.ProtocolID) {
		return fmt.Errorf("unknown response protocol ID %q", response.ProtocolID)
	}
	switch response.State {
	case ResponseStateProbing, ResponseStateForwarding, ResponseStateDiscarded:
	default:
		return fmt.Errorf("unknown response state %q", response.State)
	}
	switch response.MatchTiming {
	case MatchTimingProbing, MatchTimingForwarding:
	default:
		return fmt.Errorf("unknown match timing %q", response.MatchTiming)
	}
	if !knownBoundaryReason(response.BoundaryReason) {
		return fmt.Errorf("unknown boundary reason %q", response.BoundaryReason)
	}
	if response.VisibleToClient && !response.HeadersCommitted {
		return fmt.Errorf("client-visible result requires committed headers")
	}
	if response.State == ResponseStateDiscarded && (response.HeadersCommitted || response.ClientBodyBytesWritten != 0 || response.VisibleToClient) {
		return fmt.Errorf("discarded response cannot carry client-visible transport facts")
	}
	return nil
}

func validateRuleFacts(facts RuleFacts) error {
	if err := facts.Revision.Validate(); err != nil {
		return err
	}
	if err := facts.Winner.Rule.ID.Validate(); err != nil {
		return err
	}
	if err := facts.Winner.Rule.Action.Validate(); err != nil {
		return err
	}
	if len(facts.Matches) == 0 || len(facts.Matches) > errorrule.MaxRuleCount {
		return fmt.Errorf("matching rules must contain between 1 and %d entries", errorrule.MaxRuleCount)
	}
	if facts.Matches[0].Rule.ID != facts.Winner.Rule.ID {
		return fmt.Errorf("winner must be the first matching rule")
	}
	for _, match := range facts.Matches {
		if err := match.Rule.ID.Validate(); err != nil {
			return err
		}
	}
	if len(facts.Winner.MatchedKeywords) == 0 || len(facts.Winner.MatchedKeywords) > errorrule.MaxKeywordsPerRule {
		return fmt.Errorf("matched keywords are outside the rule bound")
	}
	if len(facts.Winner.MatchedKeywords) != len(facts.Winner.MatchedKeywordIndexes) {
		return fmt.Errorf("matched keyword values and indexes must align")
	}
	previous := -1
	for _, index := range facts.Winner.MatchedKeywordIndexes {
		if index <= previous || index < 0 || index >= len(facts.Winner.Rule.Keywords) {
			return fmt.Errorf("matched keyword indexes must be unique, ascending, and in range")
		}
		previous = index
	}
	if !canonicalFieldOrder(facts.Winner.MatchedFields) {
		return fmt.Errorf("matched fields must follow canonical semantic-field order")
	}
	return nil
}

func validateDecision(decision errorrule.Decision) error {
	switch decision.Value {
	case errorrule.DecisionPassthrough, errorrule.DecisionObserveOnly, errorrule.DecisionCommitCurrent,
		errorrule.DecisionRetrySame, errorrule.DecisionSwitchProvider, errorrule.DecisionAbortClient:
	default:
		return fmt.Errorf("unknown decision value %q", decision.Value)
	}
	if validDecisionPair(decision.Value, decision.Reason) {
		return nil
	}
	return fmt.Errorf("decision %q cannot use reason %q", decision.Value, decision.Reason)
}

func validateHealth(facts HealthFacts) error {
	switch facts.Assessment.Verdict {
	case errorrule.HealthSuccess, errorrule.HealthFailure, errorrule.HealthNeutral:
	default:
		return fmt.Errorf("unknown health verdict %q", facts.Assessment.Verdict)
	}
	if validHealthPair(facts.Assessment.Verdict, facts.Assessment.Cause) {
		return nil
	}
	return fmt.Errorf("health verdict %q cannot use cause %q", facts.Assessment.Verdict, facts.Assessment.Cause)
}

func validateSemanticConsistency(facts Facts) error {
	if facts.Alternate.Outcome == AlternateActivated && facts.Decision.Value != errorrule.DecisionSwitchProvider {
		return fmt.Errorf("activated alternate requires a switch-provider decision")
	}
	if facts.Alternate.Outcome == AlternateNotRequested && facts.Decision.Value == errorrule.DecisionSwitchProvider {
		return fmt.Errorf("switch-provider decision requires an alternate outcome")
	}
	if facts.Alternate.Outcome != AlternateActivated {
		return nil
	}
	wantReason := errorrule.SwitchReasonRuleExhausted
	if providerRejectionReason(facts.Decision.Reason) {
		wantReason = errorrule.SwitchReasonProviderUnavailable
	}
	if facts.Alternate.SwitchReason == nil || *facts.Alternate.SwitchReason != wantReason {
		return fmt.Errorf("alternate switch reason does not match the decision reason")
	}
	return nil
}

func validDecisionPair(value errorrule.DecisionValue, reason errorrule.DecisionReason) bool {
	switch value {
	case errorrule.DecisionPassthrough:
		return reason == errorrule.ReasonActionPassthrough
	case errorrule.DecisionObserveOnly:
		return reason == errorrule.ReasonObserverOnly || reason == errorrule.ReasonResponseAlreadyVisible || reason == errorrule.ReasonClientCancelled
	case errorrule.DecisionRetrySame:
		return reason == errorrule.ReasonRetryBudgetAvailable
	case errorrule.DecisionSwitchProvider:
		return reason == errorrule.ReasonReservedSwitchAttempt || reason == errorrule.ReasonRuleRetryBudgetExhausted || providerRejectionReason(reason)
	case errorrule.DecisionCommitCurrent:
		return reason == errorrule.ReasonRuleRetryBudgetExhausted || reason == errorrule.ReasonGlobalAttemptBudgetExhausted ||
			reason == errorrule.ReasonAlternateProviderUnavailable || reason == errorrule.ReasonAlternateReservationFailed || providerRejectionReason(reason)
	case errorrule.DecisionAbortClient:
		return reason == errorrule.ReasonClientRetryRequested
	default:
		return false
	}
}

func providerRejectionReason(reason errorrule.DecisionReason) bool {
	switch reason {
	case errorrule.ReasonProviderDeleted, errorrule.ReasonProviderDisabled,
		errorrule.ReasonAPIRemoved, errorrule.ReasonRoutingChanged,
		errorrule.ReasonGroupDisabled, errorrule.ReasonAuthUnavailable,
		errorrule.ReasonProviderLookupError:
		return true
	default:
		return false
	}
}

func validHealthPair(verdict errorrule.HealthVerdict, cause errorrule.HealthCause) bool {
	switch verdict {
	case errorrule.HealthSuccess:
		return cause == errorrule.HealthCauseNormalCompletion
	case errorrule.HealthFailure:
		return cause == errorrule.HealthCauseTransportFailure || cause == errorrule.HealthCauseHTTPStatusFailure || cause == errorrule.HealthCauseSemanticRetryThenSwitch
	case errorrule.HealthNeutral:
		return cause == errorrule.HealthCauseSemanticNeutral || cause == errorrule.HealthCauseClientCancelled || cause == errorrule.HealthCauseIncomplete
	default:
		return false
	}
}

func knownProtocolID(protocolID apicontract.ResponseProtocolID) bool {
	switch protocolID {
	case apicontract.ProtocolAnthropicMessagesJSON, apicontract.ProtocolAnthropicMessagesSSE,
		apicontract.ProtocolOpenAIResponsesJSON, apicontract.ProtocolOpenAIResponsesSSE,
		apicontract.ProtocolOpenAIChatCompletionsJSON, apicontract.ProtocolOpenAIChatCompletionsSSE,
		apicontract.ProtocolGoogleGenerateContentJSON, apicontract.ProtocolGoogleGenerateContentSSE:
		return true
	default:
		return false
	}
}

func validateAlternate(facts AlternateFacts) error {
	switch facts.Outcome {
	case AlternateNotRequested:
		if facts.ProviderID != nil || facts.SwitchMode != nil || facts.SwitchReason != nil {
			return fmt.Errorf("unrequested alternate cannot carry switch facts")
		}
	case AlternateActivated:
		if facts.ProviderID == nil || *facts.ProviderID == "" || facts.SwitchMode == nil || facts.SwitchReason == nil {
			return fmt.Errorf("activated alternate requires provider, mode, and reason")
		}
	case AlternateReserved:
		if facts.ProviderID == nil || *facts.ProviderID == "" {
			return fmt.Errorf("reserved alternate requires provider")
		}
	case AlternateUnavailable, AlternateFailed, AlternateReleased:
		if facts.SwitchMode != nil || facts.SwitchReason != nil {
			return fmt.Errorf("unactivated alternate cannot claim a completed switch")
		}
	default:
		return fmt.Errorf("unknown alternate outcome %q", facts.Outcome)
	}
	if facts.SwitchMode != nil && *facts.SwitchMode != SwitchModeReplacement && *facts.SwitchMode != SwitchModeFailover {
		return fmt.Errorf("unknown switch mode %q", *facts.SwitchMode)
	}
	if facts.SwitchReason != nil && *facts.SwitchReason != errorrule.SwitchReasonRuleExhausted && *facts.SwitchReason != errorrule.SwitchReasonProviderUnavailable {
		return fmt.Errorf("unknown switch reason %q", *facts.SwitchReason)
	}
	return nil
}

func knownBoundaryReason(reason responseanalysis.BoundaryReason) bool {
	switch reason {
	case responseanalysis.BoundaryNoRetryCandidate, responseanalysis.BoundaryPassthroughOnly,
		responseanalysis.BoundarySemanticMatch, responseanalysis.BoundaryClientVisibleEvent,
		responseanalysis.BoundaryProbeDurationElapsed, responseanalysis.BoundaryUpstreamEOFNoMatch,
		responseanalysis.BoundaryUpstreamReadFailure, responseanalysis.BoundaryClientCancelled,
		responseanalysis.BoundaryRequestMemoryExhausted, responseanalysis.BoundaryProcessMemoryExhausted,
		responseanalysis.BoundaryUnsupportedProtocol, responseanalysis.BoundaryUnsupportedEncoding,
		responseanalysis.BoundaryContentDecoding, responseanalysis.BoundaryMalformedFrame,
		responseanalysis.BoundaryDecodedEventTooLarge, responseanalysis.BoundarySemanticFieldTooLarge,
		responseanalysis.BoundaryAnalysisInternal:
		return true
	default:
		return false
	}
}

func canonicalFieldOrder(fields []errorrule.SemanticField) bool {
	order := map[errorrule.SemanticField]int{
		errorrule.FieldType: 0, errorrule.FieldCode: 1, errorrule.FieldMessage: 2, errorrule.FieldReason: 3,
	}
	previous := -1
	for _, field := range fields {
		current, ok := order[field]
		if !ok || current <= previous {
			return false
		}
		previous = current
	}
	return len(fields) > 0
}

func decimal(value uint64) string { return strconv.FormatUint(value, 10) }

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSwitchMode(value *SwitchMode) *SwitchMode {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSwitchReason(value *errorrule.SwitchReason) *errorrule.SwitchReason {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
