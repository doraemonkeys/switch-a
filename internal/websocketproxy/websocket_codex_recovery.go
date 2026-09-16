package websocketproxy

import (
	"context"
	"errors"
	"time"

	codexrecovery "github.com/doraemonkeys/switch-a/internal/codex/recovery"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap"
)

// WebSocket boundary failures carry transport-local classes for observability.
// The shared classifier still gets first authority so precise continuity and
// Cookie causes cannot be collapsed into these coarse adapter defaults.
func codexWebSocketRecoveryDecision(
	err error,
	phase codexrecovery.CarrierPhase,
) codexrecovery.Decision {
	fallback := codexrecovery.ConditionInternalFailure
	switch codexws.Classify(err) {
	case codexws.FailureIdentity:
		fallback = codexrecovery.ConditionStateConflict
	case codexws.FailureProtocol:
		fallback = codexrecovery.ConditionProtocolInvalid
	case codexws.FailureStorage:
		fallback = codexrecovery.ConditionStateStoreUnavailable
	}
	return codexrecovery.ClassifyWithFallback(err, phase, fallback)
}

func (o *WebSocketSessionOrchestrator) prepareCodexPhysicalDial(
	ctx context.Context,
	prepared *webSocketPreparedProviderAttempt,
) error {
	if o == nil || o.codexOperation == nil || prepared == nil {
		return nil
	}
	permit, err := o.codexOperation.PrepareDial(
		ctx, prepared.headers, prepared.candidate, prepared.applied, prepared.finalURL,
	)
	if err != nil {
		return err
	}
	prepared.boundaryPermit = permit
	applyCodexWebSocketRouteConstraint(o.selectReq, o.codexOperation)
	// Ownership discovery consumes the client's original identifiers. The same
	// frozen target then derives bytes only for this physical handshake.
	if current := o.disguise.Current(); current != nil {
		prepared.headers, err = current.Headers(ctx, prepared.headers)
		if err != nil {
			return errors.Join(err, permit.AbandonPending(ctx))
		}
		prepared.httpClient, err = o.disguise.HTTPClient()
		if err != nil {
			return errors.Join(err, permit.AbandonPending(ctx))
		}
	}
	return nil
}

func (o *WebSocketSessionOrchestrator) codexUpstreamHeaderHygiene() bool {
	return o != nil && o.codexOperation != nil
}

const (
	webSocketDialOwnershipCommit  = "commit"
	webSocketDialOwnershipAbandon = "abandon_before_disclosure"
	webSocketDialOwnershipReject  = "abandon_rejected_handshake"
)

func (o *WebSocketSessionOrchestrator) finishCodexPhysicalDial(
	ctx context.Context,
	prepared webSocketPreparedProviderAttempt,
	exchange DialExchange,
) error {
	if o == nil || o.codexOperation == nil {
		return nil
	}
	if prepared.boundaryPermit != nil {
		var err error
		decision := webSocketDialOwnershipCommit
		switch {
		case exchange.HandshakeRejected():
			// No application frames can precede an accepted upgrade. A refusal
			// releases only this dial's provisional claims, while the disclosure
			// observation and any previously established owners remain intact.
			decision = webSocketDialOwnershipReject
			err = prepared.boundaryPermit.AbandonPending(ctx)
		case exchange.Disclosure.DefinitelyNotDisclosed():
			decision = webSocketDialOwnershipAbandon
			err = prepared.boundaryPermit.AbandonPending(ctx)
		default:
			err = prepared.boundaryPermit.Commit(ctx)
		}
		o.handler.logger.Debug("websocket.dial_ownership_settled",
			zap.String("operation_id", o.requestID),
			zap.String("provider_id", prepared.candidate.RouteTargetID()),
			zap.Time("dial_started_at", exchange.StartedAt),
			zap.String("request_disclosure", exchange.Disclosure.String()),
			zap.Int("handshake_status_code", exchange.HandshakeStatusCode),
			zap.String("decision", decision), zap.Error(err))
		if err != nil {
			return err
		}
	}
	return o.codexOperation.ApplyHandshake(prepared.finalURL, exchange.HandshakeHeaders)
}

func (o *WebSocketSessionOrchestrator) newCodexBoundaryAttempt(
	provider *model.Provider,
	exchange DialExchange,
	attempt int,
	selectionMode providerSwitchMode,
	selectionMetadata selector.SelectionMetadata,
	attemptStart time.Time,
	err error,
	injectedCredential string,
) WebSocketAttemptResult {
	o.logDisguiseFailure(err)
	if exchange.Conn != nil {
		_ = exchange.Conn.Close(websocketCloseStatusForCodexFailure(err), "websocket state boundary rejected")
	}
	result := exchange.toWebSocketResult()
	result.Err = err
	result.TerminalCause = model.TerminalInternalError
	result.CommitSource = model.CommitUnknown
	o.applySessionLifecycleToResult(result)
	attemptResult := newWebSocketForwardAttemptResult(
		provider, attempt, selectionMode, selectionMetadata, result, err, time.Since(attemptStart),
	)
	attemptResult.injectedCredential = injectedCredential
	o.stampAttemptSelectionContext(&attemptResult, selectionMode, selectionMetadata)
	return attemptResult
}
