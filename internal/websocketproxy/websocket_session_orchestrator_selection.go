package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/upstreamtarget"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// Suppressed provider errors are selection evidence, not relay state. Keeping
// their replacement/fallback policy beside provider selection makes the
// post-visible no-handoff rule a single decision boundary.
func (o *WebSocketSessionOrchestrator) captureSuppressedAttempt(
	provider *model.Provider,
	relayResult *webSocketRelaySessionResult,
) {
	if relayResult == nil || relayResult.SuppressedUpstreamError == nil || provider == nil {
		return
	}

	messageType := relayResult.SuppressedMessageType
	if messageType == 0 {
		// Semantic failover only suppresses JSON application frames, so a missing
		// type means we are reconstructing legacy helper output rather than a real
		// binary payload.
		messageType = websocket.MessageText
	}

	payload := append([]byte(nil), relayResult.SuppressedMessageData...)
	if len(payload) == 0 && relayResult.SuppressedUpstreamError.Raw != "" {
		payload = []byte(relayResult.SuppressedUpstreamError.Raw)
	}

	o.suppressedAttempt = &webSocketSuppressedAttempt{
		provider:      provider,
		messageType:   messageType,
		payload:       payload,
		upstreamError: relayResult.SuppressedUpstreamError.Clone(),
	}
}

func (o *WebSocketSessionOrchestrator) clearSuppressedAttempt() { o.suppressedAttempt = nil }

func (o *WebSocketSessionOrchestrator) shouldSwitchProvider(attempt WebSocketAttemptResult) bool {
	if attempt.Result == nil {
		return false
	}
	if o.codexOperation != nil && !o.codexOperation.ReplacementAllowed() {
		return false
	}
	if codexWebSocketRecoveryDecision(attempt.terminalErr(), codexrecovery.PhaseWebSocketAccepted).Condition() == codexrecovery.ConditionReconnectRequired {
		return false
	}
	if attempt.ReplayFailed {
		return false
	}
	if o.replayBuffer != nil && !o.replayBuffer.Enabled() && attempt.Result.HandshakeAccepted {
		return false
	}
	if attempt.shouldReplaceBeforeClientVisible() {
		return true
	}
	if attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalUpstreamSemanticError {
		return o.suppressedAttempt != nil &&
			attempt.Result.UpstreamError != nil &&
			attempt.Result.UpstreamError.IsSwitchableProviderScoped()
	}
	return false
}

func (o *WebSocketSessionOrchestrator) logProviderSwitch(
	attempt WebSocketAttemptResult,
	switchReason string,
	nextSelectionMode providerSwitchMode,
) {
	if o == nil || o.handler == nil || o.handler.logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("request_id", o.requestID),
		zap.String("session_id", o.requestID),
		zap.Int("attempt_index", attempt.Attempt),
		zap.Int("provider_attempt", attempt.ProviderAttempt),
		zap.Int("provider_switch_count", attempt.ProviderSwitchCount),
		zap.String("attempt_selection_mode", string(attempt.SelectionMode)),
		zap.String("next_selection_mode", string(nextSelectionMode)),
		zap.String("switch_reason", switchReason),
		zap.String("model", o.info.Model),
	}
	if attempt.Provider != nil {
		fields = append(fields, zap.String("provider_id", attempt.Provider.ID))
	}
	if attempt.Result != nil {
		fields = append(fields,
			zap.Bool("client_visible", attempt.Result.ClientVisible),
			zap.Bool("session_committed", attempt.Result.SessionCommitted),
			zap.String("terminal_cause", string(attempt.Result.TerminalCause)),
		)
		if attempt.Result.UpstreamError != nil {
			fields = append(fields,
				zap.String("provider_error_type", attempt.Result.UpstreamError.ProviderErrorType),
				zap.String("provider_error_code", attempt.Result.UpstreamError.Code),
				zap.Int("provider_status_code", attempt.Result.UpstreamError.StatusCode),
			)
		}
	}
	o.handler.logger.Info("websocket.provider_switch", fields...)
}

func (o *WebSocketSessionOrchestrator) shouldFallbackToSuppressedPayload(attempt WebSocketAttemptResult) bool {
	if o.suppressedAttempt == nil || attempt.Result == nil || attempt.Result.ClientVisible {
		return false
	}
	if attempt.Result.TerminalCause == model.TerminalClientDisconnect ||
		attempt.Result.TerminalCause == model.TerminalInternalError {
		return false
	}
	if attempt.ReplayFailed {
		return true
	}
	return attempt.clientAccepted() && !o.shouldSwitchProvider(attempt)
}

type fallbackProviderLease struct {
	provider   *model.Provider
	generation uint64
	held       atomic.Bool
	candidate  codexidentity.CandidateSnapshot
	resolved   bool
}

func (h *Gateway) newFallbackProviderLease(provider *model.Provider, apiType string) ProviderLease {
	lease := &fallbackProviderLease{
		provider:   provider,
		generation: h.fallbackLeaseGeneration.Add(1),
	}
	if provider != nil {
		credential, ok := provider.CredentialSessionForAPIType(apiType)
		finalURL, parseErr := upstreamtarget.ParseBaseURL(provider.BaseURLForAPIType(apiType))
		if ok && credential != nil && parseErr == nil {
			lease.candidate, parseErr = codexidentity.NewAuthorityResolver().Resolve(
				credentialsession.RouteSnapshot{
					RouteTargetID: provider.ID,
					APIType:       apiType,
					VendorScope:   provider.Vendor,
					Credential:    *credential,
				},
				apiType,
				finalURL,
			)
			lease.resolved = parseErr == nil
		}
	}
	lease.held.Store(true)
	return lease
}

func (l *fallbackProviderLease) Provider() *model.Provider {
	if l == nil {
		return nil
	}
	return l.provider
}

func (l *fallbackProviderLease) ProviderID() string {
	if l == nil || l.provider == nil {
		return ""
	}
	return l.provider.ID
}

func (l *fallbackProviderLease) Generation() uint64 {
	if l == nil {
		return 0
	}
	return l.generation
}

func (l *fallbackProviderLease) CapabilityIdentity() uintptr {
	if l == nil {
		return 0
	}
	return reflect.ValueOf(l).Pointer()
}

func (l *fallbackProviderLease) CandidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	if l == nil || !l.resolved {
		return codexidentity.CandidateSnapshot{}, false
	}
	return l.candidate, true
}

func (l *fallbackProviderLease) Held() bool {
	return l != nil && l.held.Load()
}

func (l *fallbackProviderLease) Release() bool {
	return l != nil && l.held.CompareAndSwap(true, false)
}

type webSocketSelectionProbeDecision struct {
	outcome     webSocketSelectionProbeOutcome
	shouldProbe bool
}

const (
	webSocketSelectionProbeWorkUnits    = 128
	webSocketSelectionProbeDecodedBytes = 4 * 1024 * 1024
)

type webSocketProbeBudget struct {
	Duration        time.Duration
	MaxWorkUnits    int
	MaxDecodedBytes int
}

func defaultWebSocketProbeBudget() webSocketProbeBudget {
	return webSocketProbeBudget{
		Duration:        webSocketSelectionProbeTotalDuration,
		MaxWorkUnits:    webSocketSelectionProbeWorkUnits,
		MaxDecodedBytes: webSocketSelectionProbeDecodedBytes,
	}
}

type webSocketProbeBudgetTracker struct {
	budget  webSocketProbeBudget
	started time.Time
	now     func() time.Time
	frames  int
	bytes   int
}

func newWebSocketProbeBudgetTracker(budget webSocketProbeBudget, now func() time.Time) webSocketProbeBudgetTracker {
	if now == nil {
		now = time.Now
	}
	return webSocketProbeBudgetTracker{budget: budget, started: now(), now: now}
}

func (t *webSocketProbeBudgetTracker) Admit(size int) error {
	if t == nil {
		return errors.New("websocket probe budget is unavailable")
	}
	if t.now().Sub(t.started) > t.budget.Duration {
		return errors.New("websocket probe duration exceeded")
	}
	if t.frames >= t.budget.MaxWorkUnits {
		return errors.New("websocket probe frame limit exceeded")
	}
	if size < 0 || size > t.budget.MaxDecodedBytes-t.bytes {
		return errors.New("websocket probe byte limit exceeded")
	}
	t.frames++
	t.bytes += size
	return nil
}

const webSocketProbeDemandResolutionFailureMessage = "Failed to resolve hidden-model demand before provider selection"

// bootstrapSelectionContext is isolated from the main attempt loop because only
// pre-selection model consumers should read the client socket before a provider
// has been chosen.
func (o *WebSocketSessionOrchestrator) bootstrapSelectionContext(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) *WebSocketSessionResult {
	decision, err := o.selectionProbeDecision(ctx)
	o.probeOutcome = decision.outcome
	if err != nil {
		if o.handler != nil && o.handler.logger != nil {
			o.handler.logger.Error(
				"failed to resolve websocket hidden-model demand",
				zap.String("api_type", o.apiType),
				zap.Error(err),
			)
		}
		return newWebSocketProbeDecisionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			decision.outcome,
			err,
		)
	}
	if !decision.shouldProbe {
		return nil
	}

	o.subprotocol = o.subprotocol.FixForProbe()
	o.logSubprotocolDecision(
		"websocket.subprotocol_probe_fixed", webSocketSubprotocolPhaseProbeFixed, "", "", nil,
	)
	if err := o.ensureClientAccepted(w, r, o.subprotocol); err != nil {
		result := &WebSocketResult{
			Err:           err,
			TerminalCause: model.TerminalClientUpgradeRejected,
			CommitSource:  model.CommitUnknown,
		}
		return &WebSocketSessionResult{
			RequestID:      o.requestID,
			FinalResult:    result,
			FinalErr:       err,
			Attempts:       append([]WebSocketAttemptResult(nil), o.attempts...),
			IsSticky:       o.isSticky,
			ClientAccepted: false,
			ProbeOutcome:   o.probeOutcome,
		}
	}

	result, observedModel, outcome := o.probeClientSelectionContext(ctx)
	o.probeOutcome = outcome
	o.learnResolvedModel(observedModel)
	return result
}

func (o *WebSocketSessionOrchestrator) selectionProbeDecision(
	ctx context.Context,
) (webSocketSelectionProbeDecision, error) {
	if o == nil || o.selectReq == nil || o.handler == nil {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if hasUsableWebSocketSelectionModel(o.selectReq.Model) {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.probeClientModel {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	consumesHiddenModel := false
	var err error
	if !hasUsableWebSocketSelectionModel(o.selectReq.Model) && o.probeClientModel {
		consumesHiddenModel, err = o.handler.webSocketSelectionConsumesHiddenModel(ctx, o.selectReq)
	}
	if err != nil {
		// Demand resolution failures stay explicit. Treating them like "no probe
		// needed" would silently collapse pre-selection back to handshake-only
		// semantics even though a model-sensitive continuity consumer was unresolved.
		return webSocketSelectionProbeDecision{
			outcome: webSocketSelectionProbeOutcomeDemandResolutionFailed,
		}, fmt.Errorf("resolve websocket selection hidden-model demand: %w", err)
	}
	if !consumesHiddenModel {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeBypassed}, nil
	}
	if !o.supportsReplaySafeSelectionProbe() {
		return webSocketSelectionProbeDecision{outcome: webSocketSelectionProbeOutcomeUnsupported}, nil
	}
	return webSocketSelectionProbeDecision{
		outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
		shouldProbe: true,
	}, nil
}

func (o *WebSocketSessionOrchestrator) supportsReplaySafeSelectionProbe() bool {
	if o == nil {
		return false
	}
	return o.selectionProbeObserver(ModelUnknown) != nil
}

func (o *WebSocketSessionOrchestrator) selectionProbeObserver(initialModel string) WebSocketMessageObserver {
	if o == nil {
		return nil
	}
	if o.newSelectionProbeObserver != nil {
		return o.newSelectionProbeObserver(o.apiType, initialModel)
	}
	return newWebSocketMessageObserver(o.apiType, initialModel, nil, nil, nil)
}

func (o *WebSocketSessionOrchestrator) probeClientSelectionContext(
	ctx context.Context,
) (*WebSocketSessionResult, string, webSocketSelectionProbeOutcome) {
	if o.clientConn == nil {
		return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
	}
	if err := ctx.Err(); err != nil {
		return o.bootstrapProbeFailure(err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
	}
	clientConn := o.clientConn
	probeBudget := normalizedWebSocketProbeBudget(o.probeBudget)
	probeCtx, cancel := context.WithTimeout(ctx, probeBudget.Duration)
	defer cancel()
	budget := newWebSocketProbeBudgetTracker(probeBudget, o.probeNow)
	observer := o.selectionProbeObserver(o.info.Model)
	if observer == nil {
		return nil, "", webSocketSelectionProbeOutcomeUnsupported
	}

	for {
		if budget.frames >= probeBudget.MaxWorkUnits || budget.bytes >= probeBudget.MaxDecodedBytes {
			o.logProbeBudgetExhausted(&budget, "work_or_decoded_bytes")
			return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
		}
		if o.initialClientReadCh == nil {
			o.initialClientReadCh = startWebSocketInitialRead(ctx, clientConn)
		}
		initialClientRead, terminalCause, err := o.readSelectionProbeFrame(probeCtx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				o.logProbeBudgetExhausted(&budget, "duration")
				return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
			}
			return o.bootstrapProbeFailure(err, terminalCause), "", webSocketSelectionProbeOutcomeTransportFailed
		}
		messageType, data, err := initialClientRead.messageType, initialClientRead.data, initialClientRead.err
		if err != nil {
			cause := classifyRelayTerminalCause(err, webSocketPeerClient)
			return o.bootstrapProbeFailure(err, cause), "", webSocketSelectionProbeOutcomeTransportFailed
		}
		if err := budget.Admit(len(data)); err != nil {
			// This read already completed: hand it to the first live relay without parsing it.
			readCh := make(chan webSocketInitialReadResult, 1)
			readCh <- initialClientRead
			o.initialClientReadCh = readCh
			o.logProbeBudgetExhausted(&budget, err.Error())
			return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
		}

		decision, responseCreate := o.selectionProbeFrameDecision(ctx, messageType, data)
		if decision.Action == webSocketPreWriteActionReject {
			_ = o.clientConn.Close(websocketCloseStatusForCodexFailure(decision.Err), "websocket bootstrap rejected")
			return o.bootstrapProbeFailure(decision.Err, model.TerminalClientDisconnect), "", webSocketSelectionProbeOutcomeTransportFailed
		}
		o.recordSelectionProbeFrame(messageType, data, decision)

		observedModel, selectionReady := o.observeSelectionProbeFrame(observer, messageType, data, responseCreate)
		if !selectionReady {
			continue
		}
		if observedModel == "" {
			return nil, "", webSocketSelectionProbeOutcomeCompletedWithoutUsableModel
		}
		return nil, observedModel, webSocketSelectionProbeOutcomeObservedUsableModel
	}
}

// Model readiness follows the protocol's selection boundary. Parse degradation
// closes optional replay while leaving the already queued first delivery intact.
func (o *WebSocketSessionOrchestrator) observeSelectionProbeFrame(
	observer WebSocketMessageObserver,
	messageType websocket.MessageType,
	data []byte,
	responseCreate bool,
) (string, bool) {
	observer.ObserveClientMessage(messageType, data)
	observation := observer.Snapshot()
	if observation.ParseDegraded {
		o.replayBuffer.CloseReplay(webSocketReplayParseDegraded)
	}
	selectionReady := responseCreate ||
		(o.codexOperation == nil && hasUsableWebSocketSelectionModel(observation.Model))
	if !selectionReady {
		return "", false
	}
	if observation.Model == "" || observation.Model == ModelUnknown {
		return "", true
	}
	return observation.Model, true
}

func normalizedWebSocketProbeBudget(budget webSocketProbeBudget) webSocketProbeBudget {
	if budget.Duration <= 0 || budget.MaxWorkUnits <= 0 || budget.MaxDecodedBytes <= 0 {
		return defaultWebSocketProbeBudget()
	}
	return budget
}

func (o *WebSocketSessionOrchestrator) readSelectionProbeFrame(
	ctx context.Context,
) (webSocketInitialReadResult, model.TerminalCause, error) {
	select {
	case result := <-o.initialClientReadCh:
		o.initialClientReadCh = nil
		return result, model.TerminalUnknown, nil
	case <-ctx.Done():
		err := ctx.Err()
		return webSocketInitialReadResult{}, model.TerminalClientDisconnect, err
	}
}

func (o *WebSocketSessionOrchestrator) selectionProbeFrameDecision(
	ctx context.Context,
	messageType websocket.MessageType,
	data []byte,
) (webSocketPreWriteDecision, bool) {
	decision := replayableClientFrameDecision()
	if o.codexOperation != nil {
		frame := o.codexOperation.ClassifyClientFrame(ctx, messageType == websocket.MessageText, data)
		o.logCodexClientFramePermit(frame)
		decision = o.codexClientFrameDecision(ctx, frame, false)
		if decision.Action != webSocketPreWriteActionReject {
			applyCodexWebSocketRouteConstraint(o.selectReq, o.codexOperation)
		}
		return decision, frame.IsResponseCreate()
	}
	if o.apiType == APITypeCodex && messageType == websocket.MessageText {
		// Production always has a Codex operation. This fallback keeps the bounded
		// probe independently testable at the same stable event boundary.
		return decision, codexheaders.InspectClientFrame(data).EventType() == "response.create"
	}
	return decision, false
}

func (o *WebSocketSessionOrchestrator) recordSelectionProbeFrame(
	messageType websocket.MessageType,
	data []byte,
	decision webSocketPreWriteDecision,
) {
	var lineage requestcapture.MessageLineage
	if o.captureParticipates {
		lineage = o.capture.NewMessageID()
	}
	index := o.replayBuffer.RecordDecision(messageType, data, false, lineage, decision)
	message := webSocketReplayMessage{MessageType: messageType, Data: data, Lineage: lineage, Decision: decision.forReplayStorage()}
	if retained, ok := o.replayBuffer.retainForDelivery(index); ok {
		message = retained
	}
	o.pendingDelivery = append(o.pendingDelivery, webSocketPendingDelivery{message: message, replayIndex: index})
}

func (o *WebSocketSessionOrchestrator) bootstrapProbeFailure(
	err error,
	cause model.TerminalCause,
) *WebSocketSessionResult {
	result := &WebSocketResult{
		HandshakeAccepted: true,
		Err:               err,
		TerminalCause:     cause,
		CommitSource:      model.CommitUnknown,
	}
	o.applySessionLifecycleToResult(result)
	return &WebSocketSessionResult{
		RequestID: o.requestID, FinalResult: result, FinalErr: err,
		Attempts: append([]WebSocketAttemptResult(nil), o.attempts...),
		IsSticky: o.isSticky, ClientAccepted: result.ClientAccepted,
		ProbeOutcome: webSocketSelectionProbeOutcomeTransportFailed,
	}
}

func (o *WebSocketSessionOrchestrator) selectProvider(
	ctx context.Context,
	attempt int,
) (ProviderSelection, providerSwitchMode, *WebSocketSessionResult) {
	selectionMode := o.switchTracker.prepareSelection()

	selection, err := o.handler.selectProviderWithTracking(ctx, o.selectReq, attempt, o.excludedProviders)
	if err == nil {
		o.switchTracker.recordSelection(selection.Provider(), selection.Metadata)
		return selection, selectionMode, nil
	}

	if internal.IsProviderSelectionFailure(
		err,
		internal.ProviderSelectionFailureContinuityRoutingConflict,
	) {
		decision := codexrecovery.Classify(err, codexrecovery.PhaseWebSocketAccepted)
		o.handler.logger.Warn("websocket.provider_selection_rejected",
			zap.String("operation_id", o.requestID),
			zap.String("session_id", o.requestID),
			zap.String("api_type", o.apiType),
			zap.String("recovery_condition", string(decision.Condition())),
			zap.String("recovery_action", string(decision.RecoveryAction())),
			zap.String("error_code", string(decision.ErrorCode())),
			zap.Int("recovery_http_status", decision.HTTPStatus()),
		)
		return ProviderSelection{}, providerSwitchModeInitial, newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			decision.HTTPStatus(),
			model.TerminalProviderConfigurationError,
			string(decision.ErrorCode()),
			codexrecovery.ClientMessage(decision.Condition()),
			err,
		)
	}

	if errors.Is(err, internal.ErrNoProvider) {
		if len(o.attempts) > 0 {
			return ProviderSelection{}, providerSwitchModeInitial, o.finalSessionFromLastAttempt(ctx)
		}
		o.handler.logger.Warn("no providers available for websocket", zap.String("api_type", o.apiType))
		return ProviderSelection{}, providerSwitchModeInitial, newWebSocketSelectionFailureSession(
			o.requestID,
			o.isSticky,
			o.attempts,
			http.StatusServiceUnavailable,
			model.TerminalProviderUnavailable,
			ErrCodeProviderUnavailable,
			fmt.Sprintf("No available provider for api_type: %s", o.apiType),
			err,
		)
	}

	o.handler.logger.Error("provider selection failed for websocket", zap.Error(err))
	return ProviderSelection{}, providerSwitchModeInitial, newWebSocketSelectionFailureSession(
		o.requestID,
		o.isSticky,
		o.attempts,
		http.StatusInternalServerError,
		model.TerminalInternalError,
		ErrCodeInternalError,
		"Provider selection failed",
		err,
	)
}

func (o *WebSocketSessionOrchestrator) logReplayTransition(status webSocketReplayStatus) {
	if o.handler == nil || o.handler.logger == nil {
		return
	}
	o.handler.logger.Debug("websocket.replay_state",
		zap.String("operation_id", o.requestID), zap.String("session_id", o.requestID),
		zap.String("replay_state", string(status.State)), zap.Int("message_count", status.MessageCount),
		zap.Int("retained_bytes", status.RetainedBytes), zap.Int("payload_bytes", status.PayloadBytes),
		zap.Int("snapshot_bytes", status.SnapshotBytes), zap.Int64("coverage_duration_ms", status.CoverageDurationMs))
}
func (o *WebSocketSessionOrchestrator) logProbeBudgetExhausted(budget *webSocketProbeBudgetTracker, reason string) {
	if o.handler == nil || o.handler.logger == nil {
		return
	}
	o.handler.logger.Debug("websocket.selection_probe_exhausted",
		zap.String("operation_id", o.requestID), zap.String("session_id", o.requestID),
		zap.String("reason", reason), zap.Int("work_units", budget.frames),
		zap.Int("decoded_bytes", budget.bytes), zap.Duration("duration", budget.now().Sub(budget.started)))
}
