package proxy

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"

	"go.uber.org/zap"
)

type pendingHTTPResponse struct {
	head               upstreamtransport.ResponseHead
	media              responseanalysis.ResponseMedia
	pending            *responseanalysis.PendingResponse
	writer             *firstWriteResponseWriter
	exchange           httpCaptureExchange
	matcher            *semanticMatchTracker
	rules              *errorrule.CompiledRuleSet
	statsOnce          sync.Once
	snippet            *boundedSnippet
	pctx               *proxyContext
	operationID        string
	providerID         string
	logicalAttempt     uint64
	providerAttempt    uint64
	credentialPhase    attemptevidence.CredentialPhase
	analysisStartedAt  time.Time
	injectedCredential string
	codexAttempt       *codexhttp.Attempt
}

type boundedSnippet struct {
	bytes []byte
}

func (b *boundedSnippet) Observe(payload []byte) {
	if b == nil || len(b.bytes) >= maxAttemptSnippetBytes || len(payload) == 0 {
		return
	}
	remaining := maxAttemptSnippetBytes - len(b.bytes)
	if len(payload) > remaining {
		payload = payload[:remaining]
	}
	b.bytes = append(b.bytes, payload...)
}

func (b *boundedSnippet) String() string {
	if b == nil {
		return ""
	}
	return string(b.bytes)
}

func (p *pendingHTTPResponse) awaitBoundary() responseanalysis.Boundary {
	return p.pending.AwaitBoundary()
}

func (p *pendingHTTPResponse) commit(cause responseanalysis.TransitionCause) (forwardResult, error) {
	forwarding, err := p.pending.Commit(cause)
	if err != nil {
		return p.internalFailure(err), err
	}
	return p.finishForwarding(forwarding), nil
}

func (p *pendingHTTPResponse) finishForwarding(forwarding *responseanalysis.ForwardingResponse) forwardResult {
	if forwarding == nil {
		return p.internalFailure(errors.New("forwarding response is required"))
	}
	milestone := forwarding.AwaitSemanticOrCompletion()
	if milestone.Matched {
		if result, aborted := p.abortVisibleSemantic(forwarding, milestone); aborted {
			milestone.Observation.Release()
			return result
		}
		// A failed abort means completion won the race; Continue is harmless in
		// that case and required when the rule explicitly preserves the stream.
		_ = forwarding.Continue(responseanalysis.TransitionSemanticDecision)
		milestone.Observation.Release()
	}
	completion := forwarding.Wait()
	if completion.Termination == responseanalysis.TerminationCompleted {
		_ = p.writer.Finalize()
	} else {
		p.writer.DiscardBufferedSSE()
	}
	result := p.resultFromCompletion(completion)
	if completion.HasSemanticObservation {
		result.semantic = p.semanticFacts(completion.SemanticObservation, completion.State, completion.BoundaryReason)
		completion.SemanticObservation.Release()
	}
	if completion.HasUsageObservation && completion.UsageObservation.Usage != nil {
		usage := *completion.UsageObservation.Usage
		result.tokenUsage = &usage
		completion.UsageObservation.Release()
	}
	p.recordWinningRule(result.semantic)
	p.finishCapture(completion)
	return result
}

func (p *pendingHTTPResponse) abortVisibleSemantic(
	forwarding *responseanalysis.ForwardingResponse,
	milestone responseanalysis.SemanticMilestone,
) (forwardResult, bool) {
	// A semantic match can arrive after the probe has released bytes to the
	// client. If the rule opts into client-owned recovery, stop the forwarding
	// state machine immediately. Returning with an incomplete SSE stream is
	// intentional: retry-capable clients can replay the original request while
	// no provider error frame is exposed as a terminal response.
	matches, matched := p.matcher.Facts()
	if !matched || matches.Winner == nil {
		return forwardResult{}, false
	}
	decision, err := errorrule.DecideVisibleResponse(matches.Winner.Rule.Action)
	if err != nil || decision.Value != errorrule.DecisionAbortClient {
		return forwardResult{}, false
	}
	if _, err := forwarding.Discard(responseanalysis.TransitionSemanticDecision); err != nil {
		return forwardResult{}, false
	}

	completion := forwarding.Wait()
	p.writer.DiscardBufferedSSE()
	result := p.resultFromCompletion(completion)
	result.semantic = p.semanticFacts(milestone.Observation, milestone.State, completion.BoundaryReason)
	if completion.HasSemanticObservation {
		completion.SemanticObservation.Release()
	}
	if completion.HasUsageObservation && completion.UsageObservation.Usage != nil {
		usage := *completion.UsageObservation.Usage
		result.tokenUsage = &usage
		completion.UsageObservation.Release()
	}
	result.semantic.decision = decision
	p.recordWinningRule(result.semantic)
	p.finishCapture(completion)
	return result, true
}

func (p *pendingHTTPResponse) discard(
	cause responseanalysis.TransitionCause,
	reason requestcapture.TerminationReason,
	failure requestcapture.FailureObservation,
) (forwardResult, error) {
	p.writer.DiscardBufferedSSE()
	receipt, err := p.pending.Discard(cause)
	result := forwardResult{
		statusCode: p.head.StatusCode, isSSE: p.media.IsEventStream(),
		upstreamBytes: receipt.UpstreamBytesRead, decodedBytes: receipt.DecodedBytesAnalyzed,
		responseBytes:    receipt.ClientBodyBytesWritten,
		peakRequestBytes: receipt.PeakRequestBytes, peakProcessBytes: receipt.PeakProcessBytes,
		headersWritten: receipt.HeadersCommitted, responseCommitted: receipt.HeadersCommitted,
		firstByteVisible: receipt.ClientBodyBytesWritten > 0,
		boundaryReason:   receipt.BoundaryReason, analysisFailure: receipt.AnalysisFailure,
		elapsedMs: time.Since(p.analysisStartedAt).Milliseconds(), discarded: receipt.Cause != "",
		injectedCredential: p.injectedCredential,
	}
	if err != nil {
		result.failureKind = attemptFailureInternal
		result.failureMessage = err.Error()
		if termination := classifyClientTermination(p.pctx.r.Context()); termination.observed() {
			result.clientTermination = termination
			result.failureKind = attemptFailureClientTerminated
		}
	}
	p.exchange.completedAt = time.Now()
	sourceCompletion := requestcapture.SourceCompletionPartial
	if p.exchange.sourceCompletionAfterError() == requestcapture.SourceCompletionComplete {
		sourceCompletion = requestcapture.SourceCompletionComplete
	}
	p.pctx.finishHTTPCaptureExchange(p.exchange, p.head.Trailer, sourceCompletion, reason, failure)
	return result, err
}

func (p *pendingHTTPResponse) resultFromCompletion(completion responseanalysis.Completion) forwardResult {
	result := forwardResult{
		headersWritten: completion.HeadersCommitted, responseCommitted: completion.HeadersCommitted,
		firstByteVisible: completion.ClientBodyBytesWritten > 0,
		statusCode:       p.head.StatusCode, isSSE: p.media.IsEventStream(),
		responseBytes: completion.ClientBodyBytesWritten, upstreamBytes: completion.UpstreamBytesRead,
		decodedBytes:     completion.DecodedBytesAnalyzed,
		peakRequestBytes: completion.PeakRequestBytes, peakProcessBytes: completion.PeakProcessBytes,
		elapsedMs: time.Since(p.analysisStartedAt).Milliseconds(), boundaryReason: completion.BoundaryReason,
		readTermination: completion.ReadTermination, analysisFailure: completion.AnalysisFailure,
		bodySnippet: p.snippet.String(), done: true,
		injectedCredential: p.injectedCredential,
	}
	if p.media.IsEventStream() && p.writer.written && !p.writer.firstWriteTime.IsZero() {
		value := p.writer.firstWriteTime.Sub(p.pctx.startTime).Milliseconds()
		result.firstTokenMs = &value
	}
	if p.media.IsEventStream() && p.writer.sseGate != nil {
		result.responseBytes = p.writer.bytesWritten
		result.firstByteVisible = p.writer.written
	}
	switch completion.Termination {
	case responseanalysis.TerminationCompleted:
		result.success = p.head.StatusCode < defaults.StatusClientError
	case responseanalysis.TerminationDiscarded:
		// A late semantic abort intentionally closes the stream without a
		// protocol-level error. The semantic evidence carries the user-visible
		// decision; this attempt is not a successful 2xx completion.
		result.success = false
	case responseanalysis.TerminationClientCancelled:
		result.clientTermination = classifyClientTermination(p.pctx.r.Context())
		if !result.clientTermination.observed() {
			// The response coordinator can only emit this termination after its
			// request context closes. Preserve that invariant if a custom Context
			// implementation does not expose the canonical error value.
			result.clientTermination = clientTerminationDisconnect
		}
		result.failureKind = attemptFailureClientTerminated
		if result.clientTermination == clientTerminationTimeout {
			result.failureMessage = "client request deadline exceeded"
		} else {
			result.failureMessage = "client canceled response forwarding"
		}
	case responseanalysis.TerminationClientWriteFailure:
		result.failureKind = attemptFailureWrite
		result.failureMessage = "client response write failed"
		result.isClientWriteError = true
	case responseanalysis.TerminationUpstreamReadFailure:
		result.failureKind = attemptFailureRead
		if completion.ReadTermination == responseanalysis.ReadTerminationIdleTimeout {
			result.failureMessage = "upstream response idle timeout"
		} else {
			result.failureMessage = "upstream response read failed"
		}
	default:
		result.failureKind = attemptFailureInternal
		result.failureMessage = "response forwarding failed internally"
	}
	if p.writer != nil && p.writer.gateErr != nil {
		result.statusCode = http.StatusServiceUnavailable
		result.success = false
		result.failureKind = attemptFailureInternal
		result.failureMessage = p.writer.gateErr.Error()
		result.headersWritten = true
		result.responseCommitted = true
	} else if p.writer != nil && p.writer.sseGate != nil && p.writer.writeErr != nil && result.success {
		result.success = false
		result.failureKind = attemptFailureWrite
		result.failureMessage = p.writer.writeErr.Error()
		result.isClientWriteError = true
	}
	return result
}

func (p *pendingHTTPResponse) semanticFacts(
	observation responseanalysis.Observation,
	state responseanalysis.ResolutionState,
	reason responseanalysis.BoundaryReason,
) *semanticAttemptFacts {
	matches, matched := p.matcher.Facts()
	if !matched || matches.Winner == nil {
		return nil
	}
	return &semanticAttemptFacts{
		requestID: p.pctx.requestID, operationID: p.operationID, providerID: p.providerID,
		logicalAttempt: p.logicalAttempt, providerAttempt: p.providerAttempt,
		credentialPhase: p.credentialPhase,
		matches:         append([]errorrule.RuleMatch(nil), matches.All...), winner: *matches.Winner,
		protocolID: observation.ProtocolID, revision: p.rules.Revision(), windowState: state, releaseCause: reason,
		alternateOutcome: attemptevidence.AlternateNotRequested,
	}
}

func (p *pendingHTTPResponse) recordWinningRule(facts *semanticAttemptFacts) {
	if facts == nil || p == nil || p.pctx == nil {
		return
	}
	p.statsOnce.Do(func() {
		handler := p.pctx.handler
		if handler == nil || handler.ruleStats == nil {
			return
		}
		if err := handler.ruleStats.Hit(statistics.HandleFor(facts.winner.Rule), time.Now()); err != nil {
			handler.logger.Warn("failed to record internal-error rule hit",
				zap.String("request_id", p.pctx.requestID),
				zap.String("rule_id", string(facts.winner.Rule.ID)),
				zap.Error(err),
			)
		}
	})
}

func (p *pendingHTTPResponse) internalFailure(err error) forwardResult {
	message := "pending response failed"
	if err != nil {
		message = err.Error()
	}
	return forwardResult{
		statusCode: p.head.StatusCode, isSSE: p.media.IsEventStream(),
		failureKind: attemptFailureInternal, failureMessage: message,
		injectedCredential: p.injectedCredential,
	}
}
