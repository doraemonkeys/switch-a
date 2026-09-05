package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const errCodeClientDisguise = "CLIENT_DISGUISE_FAILED"

func disguiseProfileHeader(name string) bool {
	switch strings.ToLower(name) {
	case "user-agent", "originator", "version", "x-codex-client-version", "x-codex-desktop-build", "x-codex-os-version", "x-stainless-os", "x-stainless-arch", "x-stainless-package-version", "x-stainless-runtime-version":
		return true
	default:
		return false
	}
}

func (o *WebSocketSessionOrchestrator) disguiseExclusionMessage() string {
	if o.disguise == nil {
		return ""
	}
	var reasons []string
	for _, exclusion := range o.disguise.Operation().Exclusions() {
		reasons = append(reasons, exclusion.ProviderID+": "+exclusion.Reason)
	}
	if len(reasons) == 0 {
		return ""
	}
	return "No eligible provider; client disguise platform exclusions: " + strings.Join(reasons, "; ") + ". Review /admin/client-disguise."
}
func (o *WebSocketSessionOrchestrator) finishDisguiseSelection(session *WebSocketSessionResult) {
	if session == nil || o.disguise == nil || (session.FinalResult != nil && session.FinalResult.ClientDisguise != nil) {
		return
	}
	attempt := WebSocketAttemptResult{Result: session.FinalResult, ForwardErr: session.FinalErr}
	o.finishDisguiseAttempt(&attempt)
	session.FinalResult = attempt.Result
	if evidence := session.FinalResult.ClientDisguise; evidence != nil && len(evidence.Candidates) > 0 {
		evidence.Decision = "excluded"
	}
}

func (o *WebSocketSessionOrchestrator) logDisguiseFailure(err error) {
	failure := disguiseFailure(err)
	if failure == nil || o == nil || o.handler == nil || o.handler.logger == nil {
		return
	}
	o.handler.logger.Error("websocket.client_disguise_rejected", zap.String("operation_id", o.requestID),
		zap.String("diagnostic_id", failure.DiagnosticID), zap.String("stage", failure.Stage),
		zap.String("carrier", failure.Carrier), zap.String("field_path", failure.FieldPath),
		zap.String("original", failure.OriginalSnippet), zap.String("derived", failure.DerivedSnippet), zap.Error(failure))
}

func disguiseFailure(err error) *wire.Failure {
	var failure *wire.Failure
	if errors.As(err, &failure) {
		return failure
	}
	return nil
}

func disguiseRejectedWrite(err error) webSocketPreWriteDecision {
	reason := errCodeClientDisguise
	if failure := disguiseFailure(err); failure != nil {
		reason += " " + failure.DiagnosticID
	}
	return webSocketPreWriteDecision{Action: webSocketPreWriteActionReject,
		Err: &webSocketCodexCloseError{code: websocket.StatusInternalError, reason: reason, cause: err}}
}

// A conversion fault belongs to the whole logical operation. Classify it before
// the attempt loop can mistake a rejected physical write for provider failure.
func (o *WebSocketSessionOrchestrator) finishDisguiseAttempt(attempt *WebSocketAttemptResult) {
	if o == nil || attempt == nil {
		return
	}
	failure := disguiseFailure(attempt.terminalErr())
	if failure != nil {
		if attempt.Result == nil {
			attempt.Result = &WebSocketResult{}
		}
		attempt.Result.Err = attempt.terminalErr()
		attempt.Result.TerminalCause = model.TerminalInternalError
		attempt.Result.UpstreamError = nil
		attempt.ForwardErr = attempt.Result.Err
		attempt.GatewayStatusCode = http.StatusInternalServerError
		attempt.GatewayErrorCode = errCodeClientDisguise
		attempt.GatewayMessage = "Client disguise transformation failed; diagnostic " + failure.DiagnosticID
	}
	if o.disguise == nil {
		return
	}
	evidence := &attemptevidence.ClientDisguise{DiagnosticID: uuid.NewString(),
		RequestID: o.requestID, OperationID: o.requestID, Decision: "disabled"}
	if o.codexOperation != nil {
		evidence.ClientIdentityID = o.codexOperation.ClientIdentity().ID
	}
	evidence.PlatformFacts = make(map[string]string)
	for _, fact := range o.disguise.Operation().Facts().Evidence {
		evidence.PlatformFacts[fact.Field] = fact.Value
	}
	o.appendDisguiseTargetEvidence(evidence, attempt.Provider, failure)
	o.appendDisguiseCandidateEvidence(evidence)
	o.appendDisguiseDifferenceEvidence(evidence)
	if failure != nil {
		evidence.DiagnosticID = failure.DiagnosticID
		evidence.Decision = "failed"
		evidence.Phase = failure.Stage
		detail := &attemptevidence.DisguiseFailure{Phase: failure.Stage, Location: failure.Carrier + " " + failure.FieldPath,
			OriginalSnippet: failure.OriginalSnippet, DerivedSnippet: failure.DerivedSnippet}
		for err := error(failure); err != nil; err = errors.Unwrap(err) {
			detail.ErrorChain = append(detail.ErrorChain, err.Error())
		}
		evidence.Failure = detail
	}
	if attempt.Result == nil {
		attempt.Result = &WebSocketResult{}
	}
	attempt.Result.ClientDisguise = evidence
	o.logDisguiseAttempt(attempt, evidence, failure)
}

func (o *WebSocketSessionOrchestrator) appendDisguiseTargetEvidence(evidence *attemptevidence.ClientDisguise, provider *model.Provider, failure *wire.Failure) {
	if provider == nil {
		return
	}
	evidence.ProviderID = provider.ID
	if credential, ok := provider.CredentialSessionForAPIType(APITypeCodex); ok {
		evidence.CredentialSessionID = credential.SessionID
	}
	target, ok := o.disguise.Operation().Target(evidence.ProviderID, evidence.CredentialSessionID)
	if !ok {
		return
	}
	if target.Policy.Enabled {
		evidence.Decision = "transformed"
	}
	evidence.GenerationID = target.Login.GenerationID
	evidence.DeviceID = target.Login.DeviceID
	if target.Login.AccountBasis.Kind == "account" {
		evidence.AccountID = string(target.Login.AccountBasis.Value)
	}
	evidence.ClientVersion = target.Profile.ClientVersion
	evidence.RevisionID = target.Profile.ID
	evidence.SourceID = target.Profile.SourceID
	if !target.Profile.CapturedAt.IsZero() {
		evidence.CapturedAt = target.Profile.CapturedAt.Format(time.RFC3339Nano)
	}
	evidence.ClientType, evidence.Platform, evidence.Arch = target.Profile.Tuple.ClientType, target.Profile.Tuple.Platform, target.Profile.Tuple.Arch
	if target.Transport != nil {
		evidence.TransportSampleID = target.Transport.ID
		if failure == nil || failure.Stage != "transport" {
			evidence.AppliedScopes = append(evidence.AppliedScopes, "transport")
		}
	}
}

func (o *WebSocketSessionOrchestrator) appendDisguiseCandidateEvidence(evidence *attemptevidence.ClientDisguise) {
	for _, excluded := range o.disguise.Operation().Exclusions() {
		evidence.Candidates = append(evidence.Candidates, attemptevidence.DisguiseCandidate{
			ProviderID: excluded.ProviderID, CredentialSessionID: excluded.CredentialSessionID,
			Outcome: "excluded", Reason: excluded.Reason, Platform: excluded.Decision.Facts.Tuple.Platform})
		if evidence.PlatformFacts == nil {
			evidence.PlatformFacts = make(map[string]string)
		}
		for _, fact := range excluded.Decision.Facts.Evidence {
			evidence.PlatformFacts[fact.Field] = fact.Value
		}
	}
}

func (o *WebSocketSessionOrchestrator) appendDisguiseDifferenceEvidence(evidence *attemptevidence.ClientDisguise) {
	current := o.disguise.Current()
	if current == nil {
		return
	}
	profileApplied, identifiersApplied := false, false
	for _, difference := range current.Differences() {
		evidence.Differences = append(evidence.Differences, attemptevidence.DisguiseDifference{
			Carrier: difference.Carrier, Location: difference.FieldPath, Original: difference.Original, Derived: difference.Derived})
		if difference.Carrier == "header" && disguiseProfileHeader(difference.FieldPath) {
			profileApplied = true
		} else {
			identifiersApplied = true
		}
	}
	if profileApplied {
		evidence.AppliedScopes = append(evidence.AppliedScopes, "application_profile")
	}
	if identifiersApplied {
		evidence.AppliedScopes = append(evidence.AppliedScopes, "identifiers")
	}
}

func (o *WebSocketSessionOrchestrator) logDisguiseAttempt(attempt *WebSocketAttemptResult, evidence *attemptevidence.ClientDisguise, failure *wire.Failure) {
	if o.handler == nil || o.handler.logger == nil {
		return
	}
	baseResult := *attempt.Result
	baseResult.ClientDisguise = nil
	baseAttempt := *attempt
	baseAttempt.Result = &baseResult
	if _, err := attemptevidence.EncodeClientDisguiseString(buildWebSocketAttemptEvidence(baseAttempt), evidence); err != nil {
		o.handler.logger.Error("websocket.client_disguise_evidence_encoding_failed",
			zap.String("operation_id", o.requestID), zap.String("diagnostic_id", evidence.DiagnosticID), zap.Error(err))
	}
	fields := []zap.Field{zap.String("operation_id", o.requestID), zap.Int("attempt_index", attempt.Attempt),
		zap.String("provider_id", evidence.ProviderID), zap.Any("client_disguise", evidence)}
	if failure != nil {
		o.handler.logger.Error("websocket.client_disguise_failed", fields...)
	} else {
		o.handler.logger.Debug("websocket.client_disguise_completed", fields...)
	}
}

// Losing the first-binding race changes eligibility, not a logical attempt.
// Only the final lease may persist a profile; superseded sticky/active previews
// remain read-only and never acquire a login identity as a side effect.
func (o *WebSocketSessionOrchestrator) selectPhysicalTarget(ctx context.Context, attempt int) (ProviderSelection, error) {
	excluded := make(map[string]bool, len(o.excludedProviders))
	for id, value := range o.excludedProviders {
		excluded[id] = value
	}
	for {
		if err := ctx.Err(); err != nil {
			return ProviderSelection{}, err
		}
		var selection ProviderSelection
		var err error
		if o.codexOperation != nil && o.codexOperation.AllowsAccountSwitch() && attempt == 0 && len(excluded) == 0 && o.handler.selector != nil {
			selection, err = normalizeProviderSelection(o.handler.selector.SelectInitial(ctx, o.selectReq))
		} else {
			selection, err = o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, excluded)
		}
		if err != nil || o.disguise == nil {
			return selection, err
		}
		_, err = o.disguise.Operation().Commit(ctx, selection.Provider())
		if err == nil {
			return selection, nil
		}
		selection.Lease.Release()
		if !errors.Is(err, clientdisguise.ErrCandidateExcluded) {
			return ProviderSelection{}, err
		}
		excluded[selection.Provider().ID] = true
		o.handler.logger.Debug("websocket.client_disguise_target_reselected", zap.String("operation_id", o.requestID), zap.String("provider_id", selection.Provider().ID), zap.Error(err))
	}
}
