package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"

	"go.uber.org/zap"
)

const x3TestRequestID = "x3-request"

func TestX3ForwardResultIsFactOnly(t *testing.T) {
	t.Parallel()

	facts := reflect.TypeOf(forwardResult{})
	if facts.Kind() != reflect.Struct {
		t.Fatalf("forwardResult kind = %s, want struct", facts.Kind())
	}
	bannedCapabilities := []reflect.Type{
		reflect.TypeOf((*error)(nil)).Elem(),
		reflect.TypeOf((*io.ReadCloser)(nil)).Elem(),
		reflect.TypeOf((*http.ResponseWriter)(nil)).Elem(),
		reflect.TypeOf((*providerLease)(nil)).Elem(),
		reflect.TypeOf((*retryPermit)(nil)).Elem(),
		reflect.TypeOf((*alternateProviderReservation)(nil)).Elem(),
	}
	allowedPointers := map[string]bool{"firstTokenMs": true, "tokenUsage": true, "semantic": true}
	for index := 0; index < facts.NumField(); index++ {
		field := facts.Field(index)
		if field.Anonymous {
			t.Fatalf("forwardResult embeds %s; facts must remain explicit", field.Type)
		}
		if field.Type.Kind() == reflect.Chan || field.Type.Kind() == reflect.Func || field.Type.Kind() == reflect.Interface {
			t.Fatalf("forwardResult.%s retains live capability kind %s", field.Name, field.Type.Kind())
		}
		if field.Type.Kind() == reflect.Pointer && !allowedPointers[field.Name] {
			t.Fatalf("forwardResult.%s is unexpected pointer %s", field.Name, field.Type)
		}
		for _, capability := range bannedCapabilities {
			if field.Type == capability || field.Type.Implements(capability) {
				t.Fatalf("forwardResult.%s retains capability %s", field.Name, capability)
			}
		}
		name := field.Type.String()
		if strings.Contains(name, "PendingResponse") || strings.Contains(name, "upstreamtransport.Response") {
			t.Fatalf("forwardResult.%s retains response owner %s", field.Name, name)
		}
	}

	owner := reflect.TypeOf(pendingHTTPResponse{})
	pendingOwners := 0
	for index := 0; index < owner.NumField(); index++ {
		field := owner.Field(index)
		if field.Type == reflect.TypeOf((*responseanalysis.PendingResponse)(nil)) {
			pendingOwners++
		}
		if field.Type == reflect.TypeOf((*upstreamtransport.Response)(nil)) || field.Type.Implements(reflect.TypeOf((*io.ReadCloser)(nil)).Elem()) {
			t.Fatalf("pendingHTTPResponse.%s bypasses the sole PendingResponse owner", field.Name)
		}
	}
	if pendingOwners != 1 {
		t.Fatalf("pendingHTTPResponse owner count = %d, want 1", pendingOwners)
	}
}

func TestX3ExecutionPrecedence(t *testing.T) {
	rules := x3CompiledRuleSet(t, 1, x3RetryThenSwitchAction(t, 1), "retry-me")

	t.Run("transport failure stops before response analysis", func(t *testing.T) {
		events := &x3EventLog{}
		provider := x3Provider("p1")
		lease := x3NewLease(provider, events)
		selection := &x3Selector{initial: provider, initialLease: lease, events: events}
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{{err: errors.New("dial refused")}}}
		analyzer := x3AnalyzerSpyForTest(t)
		health := newX3Health()
		auth := &x3Auth{refresh: true, events: events}
		stats := &x3RuleStats{}
		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: analyzer, health: health,
			auth: auth, stats: stats, globalMaxAttempts: 1,
		})

		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
		}
		if analyzer.Count() != 0 || auth.RefreshCount() != 0 || stats.Count() != 0 {
			t.Fatalf("analysis=%d refresh=%d stats=%d", analyzer.Count(), auth.RefreshCount(), stats.Count())
		}
		if health.Failures("p1") != 1 || health.Successes("p1") != 0 {
			t.Fatalf("health failures=%d successes=%d", health.Failures("p1"), health.Successes("p1"))
		}
		if len(pctx.attempts) != 1 || lease.ReleaseCount() != 1 {
			t.Fatalf("attempts=%d lease releases=%d", len(pctx.attempts), lease.ReleaseCount())
		}
	})

	t.Run("credential refresh is a subexchange before status policy", func(t *testing.T) {
		events := &x3EventLog{}
		provider := x3Provider("p1")
		lease := x3NewLease(provider, events)
		selection := &x3Selector{initial: provider, initialLease: lease, events: events}
		unauthorized := []byte(`{"type":"error","error":{"message":"retry-me"}}`)
		accepted := []byte(`{"id":"accepted"}`)
		firstBody := x3NewTrackedBody(unauthorized, "close:unauthorized", events)
		secondBody := x3NewTrackedBody(accepted, "close:accepted", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusUnauthorized, "application/json", "", firstBody, len(unauthorized)),
			x3HTTPResponseStep(http.StatusOK, "application/json", "", secondBody, len(accepted)),
		}}
		analyzer := x3AnalyzerSpyForTest(t)
		health := newX3Health()
		auth := &x3Auth{refresh: true, events: events}
		stats := &x3RuleStats{}
		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: analyzer, health: health,
			auth: auth, stats: stats, globalMaxAttempts: 1,
		})

		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), accepted) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		if transport.Count() != 2 || analyzer.Count() != 2 || auth.ApplyCount() != 2 || auth.RefreshCount() != 1 {
			t.Fatalf("fetch=%d analysis=%d apply=%d refresh=%d", transport.Count(), analyzer.Count(), auth.ApplyCount(), auth.RefreshCount())
		}
		if len(pctx.attempts) != 1 {
			t.Fatalf("credential refresh consumed %d logical attempts, want 1", len(pctx.attempts))
		}
		if stats.Count() != 0 || health.Failures("p1") != 0 || health.Successes("p1") != 1 {
			t.Fatalf("stats=%d failures=%d successes=%d", stats.Count(), health.Failures("p1"), health.Successes("p1"))
		}
		operationIDs := analyzer.OperationIDs()
		if len(operationIDs) != 2 || operationIDs[0] == operationIDs[1] ||
			!strings.HasSuffix(operationIDs[0], "/credential/initial") ||
			!strings.HasSuffix(operationIDs[1], "/credential/refreshed") {
			t.Fatalf("credential subexchange operation IDs = %v", operationIDs)
		}
		if firstBody.CloseCount() != 1 || secondBody.CloseCount() != 1 || lease.ReleaseCount() != 1 {
			t.Fatalf("body closes=(%d,%d) lease releases=%d", firstBody.CloseCount(), secondBody.CloseCount(), lease.ReleaseCount())
		}
		events.RequireOrder(t, "auth:apply", "fetch:p1.example", "auth:refresh", "auth:apply", "fetch:p1.example")
		events.RequireOrder(t, "auth:refresh", "dispatch:reserve", "close:unauthorized", "dispatch:activate", "fetch:p1.example")
	})

	t.Run("HTTP status policy wins over semantic-looking payload", func(t *testing.T) {
		events := &x3EventLog{}
		provider := x3Provider("p1")
		lease := x3NewLease(provider, events)
		selection := &x3Selector{initial: provider, initialLease: lease, events: events}
		wire := []byte(`{"type":"error","error":{"message":"retry-me"}}`)
		body := x3NewTrackedBody(wire, "close:status", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusServiceUnavailable, "application/json", "", body, len(wire)),
		}}
		analyzer := x3AnalyzerSpyForTest(t)
		health := newX3Health()
		stats := &x3RuleStats{}
		recorder, _ := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: analyzer, health: health,
			stats: stats, globalMaxAttempts: 1,
		})

		if recorder.Code != http.StatusServiceUnavailable || !bytes.Equal(recorder.Body.Bytes(), wire) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		if analyzer.Count() != 1 || stats.Count() != 0 {
			t.Fatalf("analysis=%d semantic stats=%d", analyzer.Count(), stats.Count())
		}
		if health.Failures("p1") != 1 || health.Successes("p1") != 0 {
			t.Fatalf("health failures=%d successes=%d", health.Failures("p1"), health.Successes("p1"))
		}
		if body.CloseCount() != 1 || lease.ReleaseCount() != 1 {
			t.Fatalf("body closes=%d lease releases=%d", body.CloseCount(), lease.ReleaseCount())
		}
	})
}

func TestX3LegacyRetryUsesGenerationBoundDispatchPermit(t *testing.T) {
	t.Run("activation authorizes retry only after discard", func(t *testing.T) {
		events := &x3EventLog{}
		provider := x3Provider("p1")
		provider.MaxRetries = 1
		lease := x3NewLease(provider, events)
		selection := &x3Selector{initial: provider, initialLease: lease, events: events}
		firstWire := []byte(`{"error":"temporarily unavailable"}`)
		secondWire := []byte(`{"id":"legacy-retry-success"}`)
		firstBody := x3NewTrackedBody(firstWire, "close:legacy-first", events)
		secondBody := x3NewTrackedBody(secondWire, "close:legacy-second", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusInternalServerError, "application/json", "", firstBody, len(firstWire)),
			x3HTTPResponseStep(http.StatusOK, "application/json", "", secondBody, len(secondWire)),
		}}
		rules, err := errorrule.CompileRuleSet(60, nil)
		if err != nil {
			t.Fatal(err)
		}

		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
			stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		if transport.Count() != 2 || len(pctx.attempts) != 2 {
			t.Fatalf("fetch=%d attempts=%d", transport.Count(), len(pctx.attempts))
		}
		if firstBody.CloseCount() != 1 || secondBody.CloseCount() != 1 || lease.ReleaseCount() != 1 {
			t.Fatalf("body closes=(%d,%d) lease releases=%d", firstBody.CloseCount(), secondBody.CloseCount(), lease.ReleaseCount())
		}
		events.RequireOrder(t, "dispatch:reserve", "close:legacy-first", "dispatch:activate", "fetch:p1.example")
	})

	t.Run("retired generation cannot redispatch after discard", func(t *testing.T) {
		events := &x3EventLog{}
		provider := x3Provider("p1")
		provider.MaxRetries = 1
		lease := x3NewLease(provider, events)
		selection := &x3Selector{
			initial: provider, initialLease: lease, events: events,
			dispatchActivateErr: &selector.ProviderRejectionError{Reason: errorrule.ReasonProviderDeleted},
		}
		wire := []byte(`{"error":"temporarily unavailable"}`)
		body := x3NewTrackedBody(wire, "close:retired", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusInternalServerError, "application/json", "", body, len(wire)),
		}}
		rules, err := errorrule.CompileRuleSet(61, nil)
		if err != nil {
			t.Fatal(err)
		}

		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
			stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
		}
		if transport.Count() != 1 || len(pctx.attempts) != 1 {
			t.Fatalf("stale redispatch occurred: fetch=%d attempts=%d", transport.Count(), len(pctx.attempts))
		}
		if body.CloseCount() != 1 || lease.ReleaseCount() != 1 {
			t.Fatalf("body closes=%d lease releases=%d", body.CloseCount(), lease.ReleaseCount())
		}
		events.RequireOrder(t, "dispatch:reserve", "close:retired", "dispatch:reject", "lease:release:p1")
	})

	t.Run("live rejection switches with provider unavailable reason", func(t *testing.T) {
		events := &x3EventLog{}
		primary := x3Provider("p1")
		primary.MaxRetries = 1
		alternate := x3Provider("p2")
		primaryLease := x3NewLease(primary, events)
		selection := &x3Selector{
			initial: primary, initialLease: primaryLease, alternate: alternate, events: events,
			dispatchReserveErr: &selector.ProviderRejectionError{Reason: errorrule.ReasonProviderDisabled},
		}
		failedWire := []byte(`{"error":"temporarily unavailable"}`)
		successWire := []byte(`{"id":"alternate-success"}`)
		failedBody := x3NewTrackedBody(failedWire, "close:rejected-primary", events)
		successBody := x3NewTrackedBody(successWire, "close:accepted-alternate", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusInternalServerError, "application/json", "", failedBody, len(failedWire)),
			x3HTTPResponseStep(http.StatusOK, "application/json", "", successBody, len(successWire)),
		}}
		rules, err := errorrule.CompileRuleSet(62, nil)
		if err != nil {
			t.Fatal(err)
		}

		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{primary, alternate}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
			stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		if transport.Count() != 2 || len(pctx.attempts) != 2 {
			t.Fatalf("fetch=%d attempts=%d", transport.Count(), len(pctx.attempts))
		}
		if pctx.attempts[0].SwitchReason != string(errorrule.SwitchReasonProviderUnavailable) {
			t.Fatalf("switch reason = %q, want %q", pctx.attempts[0].SwitchReason, errorrule.SwitchReasonProviderUnavailable)
		}
		if failedBody.CloseCount() != 1 || successBody.CloseCount() != 1 {
			t.Fatalf("body closes=(%d,%d)", failedBody.CloseCount(), successBody.CloseCount())
		}
	})
}

func TestX3SemanticRetryReservesBeforeDiscardAndChargesAtActivation(t *testing.T) {
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selection := &x3Selector{initial: provider, initialLease: lease, events: events}
	firstWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\"}\r\n\r\n")
	secondWire := []byte(`{"id":"successful-retry"}`)
	firstBody := x3NewBlockingBody(firstWire, "close:first", events)
	secondBody := x3NewTrackedBody(secondWire, "close:second", events)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", firstBody, len(firstWire)),
		x3HTTPResponseStep(http.StatusOK, "application/json", "", secondBody, len(secondWire)),
	}}
	rules := x3CompiledRuleSet(t, 11, x3RetryOnlyAction(t, 1), "retry-me")
	health := newX3Health()
	stats := &x3RuleStats{}
	recorder, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selection, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: health,
		stats: stats, backoff: x3Backoff{events: events}, globalMaxAttempts: 2,
	})

	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	permit := selection.Permit()
	if permit == nil {
		t.Fatal("same-provider retry permit was not reserved")
	}
	if permit.LedgerBefore().LogicalAttemptsStarted() != 1 || permit.LedgerAfter().LogicalAttemptsStarted() != 2 {
		t.Fatalf("ledger attempts before=%d after=%d", permit.LedgerBefore().LogicalAttemptsStarted(), permit.LedgerAfter().LogicalAttemptsStarted())
	}
	if len(pctx.attempts) != 2 || transport.Count() != 2 || stats.Count() != 1 {
		t.Fatalf("attempts=%d fetch=%d stats=%d", len(pctx.attempts), transport.Count(), stats.Count())
	}
	if firstBody.CloseCount() != 1 || secondBody.CloseCount() != 1 || lease.ReleaseCount() != 1 {
		t.Fatalf("body closes=(%d,%d) lease releases=%d", firstBody.CloseCount(), secondBody.CloseCount(), lease.ReleaseCount())
	}
	if health.Failures("p1") != 0 || health.Successes("p1") != 1 {
		t.Fatalf("health failures=%d successes=%d", health.Failures("p1"), health.Successes("p1"))
	}
	events.RequireOrder(t, "backoff", "retry:reserve", "close:first", "retry:activate", "fetch:p1.example", "close:second", "lease:release:p1")
}

func TestX3RetryThenSwitchReservationAndRollback(t *testing.T) {
	t.Run("final global slot is reserved for an alternate", func(t *testing.T) {
		events := &x3EventLog{}
		primary := x3Provider("p1")
		alternate := x3Provider("p2")
		primaryLease := x3NewLease(primary, events)
		selection := &x3Selector{initial: primary, initialLease: primaryLease, alternate: alternate, events: events}
		firstWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"switch-me\"}\r\n\r\n")
		secondWire := []byte(`{"id":"alternate-success"}`)
		firstBody := x3NewBlockingBody(firstWire, "close:primary", events)
		secondBody := x3NewTrackedBody(secondWire, "close:alternate", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", firstBody, len(firstWire)),
			x3HTTPResponseStep(http.StatusOK, "application/json", "", secondBody, len(secondWire)),
		}}
		rules := x3CompiledRuleSet(t, 21, x3RetryThenSwitchAction(t, 3), "switch-me")
		health := newX3Health()
		analyzer := x3AnalyzerSpyForTest(t)
		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{primary, alternate}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: analyzer, health: health,
			stats: &x3RuleStats{}, backoff: x3Backoff{events: events}, globalMaxAttempts: 2,
		})

		reservation := selection.Reservation()
		if reservation == nil {
			t.Fatal("alternate was not reserved")
		}
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		if len(pctx.attempts) != 2 || pctx.attempts[0].ProviderID != "p1" || pctx.attempts[1].ProviderID != "p2" {
			t.Fatalf("attempt chain = %#v", pctx.attempts)
		}
		if selection.SameRetryReservations() != 0 || reservation.ActivationCount() != 1 || reservation.ReleaseCount() != 0 {
			t.Fatalf("same retries=%d activation=%d reservation releases=%d", selection.SameRetryReservations(), reservation.ActivationCount(), reservation.ReleaseCount())
		}
		if primaryLease.ReleaseCount() != 1 || reservation.Lease().ReleaseCount() != 1 {
			t.Fatalf("lease releases primary=%d alternate=%d", primaryLease.ReleaseCount(), reservation.Lease().ReleaseCount())
		}
		if health.Failures("p1") != 1 || health.Successes("p2") != 1 {
			t.Fatalf("health p1 failures=%d p2 successes=%d", health.Failures("p1"), health.Successes("p2"))
		}
		operationIDs := analyzer.OperationIDs()
		if len(operationIDs) != 2 || operationIDs[0] == operationIDs[1] ||
			!strings.Contains(operationIDs[0], "/attempt/0/provider/p1/") ||
			!strings.Contains(operationIDs[1], "/attempt/1/provider/p2/") {
			t.Fatalf("analysis operation IDs = %v, want unique logical-attempt/provider identities", operationIDs)
		}
		events.RequireOrder(t, "alternate:reserve", "alternate:prepare", "close:primary", "alternate:activate", "lease:release:p1", "fetch:p2.example", "close:alternate", "lease:release:p2")
	})

	t.Run("failed preview leaves live continuity unchanged", func(t *testing.T) {
		events := &x3EventLog{}
		primary := x3Provider("p1")
		invalidAlternate := x3Provider("")
		primaryLease := x3NewLease(primary, events)
		selection := &x3Selector{initial: primary, initialLease: primaryLease, alternate: invalidAlternate, events: events}
		wire := []byte(`{"type":"error","error":{"message":"switch-me"}}`)
		body := x3NewTrackedBody(wire, "close:current", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusOK, "application/json", "", body, len(wire)),
		}}
		rules := x3CompiledRuleSet(t, 22, x3RetryThenSwitchAction(t, 0), "switch-me")
		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{primary}, selector: selection, transport: transport,
			rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
			stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		reservation := selection.Reservation()
		if reservation == nil {
			t.Fatal("alternate reservation was not created")
		}
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), wire) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		history := pctx.selectReq.ProviderSwitchHistory
		if history == nil || history.ProviderSwitchCount != 0 || len(history.AttemptChain) != 1 || history.AttemptChain[0] != "p1" {
			t.Fatalf("live switch history mutated by failed preview: %#v", history)
		}
		if reservation.ActivationCount() != 0 || reservation.ReleaseCount() != 1 || transport.Count() != 1 {
			t.Fatalf("activation=%d reservation releases=%d fetch=%d", reservation.ActivationCount(), reservation.ReleaseCount(), transport.Count())
		}
		if primaryLease.ReleaseCount() != 1 || reservation.Lease().ReleaseCount() != 1 || body.CloseCount() != 1 {
			t.Fatalf("lease releases=(%d,%d) body closes=%d", primaryLease.ReleaseCount(), reservation.Lease().ReleaseCount(), body.CloseCount())
		}
		events.RequireOrder(t, "alternate:reserve", "alternate:prepare", "alternate:release", "close:current", "lease:release:p1")
	})
}

func TestX3RuleSetIsPinnedAcrossRetries(t *testing.T) {
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selection := &x3Selector{initial: provider, initialLease: lease, events: events}
	pinned := x3CompiledRuleSet(t, 31, x3RetryOnlyAction(t, 1), "retry-me")
	replacement, err := errorrule.CompileRuleSet(32, nil)
	if err != nil {
		t.Fatal(err)
	}
	ruleProvider := &x3RuleProvider{current: pinned}
	firstWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\"}\r\n\r\n")
	secondWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\",\"attempt\":2}\r\n\r\n")
	firstBody := x3NewBlockingBody(firstWire, "close:first", events)
	secondBody := x3NewTrackedBody(secondWire, "close:second", events)
	firstStep := x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", firstBody, len(firstWire))
	firstStep.onFetch = func() { ruleProvider.Set(replacement) }
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		firstStep,
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", secondBody, len(secondWire)),
	}}
	stats := &x3RuleStats{}
	recorder, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selection, transport: transport,
		rules: ruleProvider, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: stats,
		globalMaxAttempts: 2,
	})

	if ruleProvider.Calls() != 1 {
		t.Fatalf("CurrentRuleSet calls = %d, want exactly 1", ruleProvider.Calls())
	}
	if stats.Count() != 2 || len(pctx.attempts) != 2 || transport.Count() != 2 {
		t.Fatalf("stats=%d attempts=%d fetch=%d", stats.Count(), len(pctx.attempts), transport.Count())
	}
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	if firstBody.CloseCount() != 1 || secondBody.CloseCount() != 1 || lease.ReleaseCount() != 1 {
		t.Fatalf("body closes=(%d,%d) lease releases=%d", firstBody.CloseCount(), secondBody.CloseCount(), lease.ReleaseCount())
	}
}

func TestX3ExhaustedResponsePreservesWireIdentity(t *testing.T) {
	decoded := []byte(`{"type":"error","error":{"message":"retry-me"}}`)
	tests := []struct {
		name     string
		encoding string
		wire     func(*testing.T) []byte
	}{
		{name: "identity", wire: func(*testing.T) []byte { return append([]byte(nil), decoded...) }},
		{name: "gzip", encoding: "gzip", wire: func(t *testing.T) []byte { return x3Gzip(t, decoded) }},
		{name: "brotli fail-open", encoding: "br", wire: func(*testing.T) []byte { return append([]byte(nil), decoded...) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := &x3EventLog{}
			provider := x3Provider("p1")
			lease := x3NewLease(provider, events)
			selection := &x3Selector{initial: provider, initialLease: lease, events: events}
			wire := test.wire(t)
			body := x3NewTrackedBody(wire, "close:wire", events)
			step := x3HTTPResponseStep(http.StatusOK, "application/json", test.encoding, body, len(wire))
			step.header.Add("X-Repeat", "one")
			step.header.Add("X-Repeat", "two")
			transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{step}}
			rules := x3CompiledRuleSet(t, 41, x3RetryThenSwitchAction(t, 0), "retry-me")
			recorder, _ := x3Execute(t, x3ExecutionConfig{
				providers: []*model.Provider{provider}, selector: selection, transport: transport,
				rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
				stats: &x3RuleStats{}, globalMaxAttempts: 1,
			})

			if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), wire) {
				t.Fatalf("status=%d body=%x want=%x", recorder.Code, recorder.Body.Bytes(), wire)
			}
			if got := recorder.Header().Get("Content-Encoding"); got != test.encoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.encoding)
			}
			if got := recorder.Header().Values("X-Repeat"); !reflect.DeepEqual(got, []string{"one", "two"}) {
				t.Fatalf("repeated header = %#v", got)
			}
			if body.CloseCount() != 1 || lease.ReleaseCount() != 1 {
				t.Fatalf("body closes=%d lease releases=%d", body.CloseCount(), lease.ReleaseCount())
			}
		})
	}
}

func TestX3FlagshipThirdSSEErrorAt72KiBIsDiscarded(t *testing.T) {
	const thirdEventBytes = 72 * 1024
	prefix := "event: error\r\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are overloaded at capacity\"}\r\n\r\n"
	third := prefix + strings.Repeat("x", thirdEventBytes-len(prefix)-len(suffix)) + suffix
	firstWire := []byte(
		"event: response.created\r\ndata: {\"type\":\"response.created\"}\r\n\r\n" +
			"event: response.in_progress\r\ndata: {\"type\":\"response.in_progress\"}\r\n\r\n" + third,
	)
	secondWire := []byte("event: response.completed\r\ndata: {\"type\":\"response.completed\"}\r\n\r\n")

	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selection := &x3Selector{initial: provider, initialLease: lease, events: events}
	firstBody := x3NewBlockingBody(firstWire, "close:flagship", events)
	secondBody := x3NewTrackedBody(secondWire, "close:replacement", events)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", firstBody, len(firstWire)),
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", secondBody, len(secondWire)),
	}}
	rules := x3CompiledRuleSet(t, 51, x3RetryOnlyAction(t, 1), "overloaded at capacity")
	stats := &x3RuleStats{}
	recorder, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selection, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
		stats: stats, globalMaxAttempts: 2,
	})

	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
		t.Fatalf("status=%d body bytes=%d want=%d", recorder.Code, recorder.Body.Len(), len(secondWire))
	}
	if !recorder.Flushed {
		t.Fatal("committed SSE response was not flushed")
	}
	if len(pctx.attempts) != 2 || transport.Count() != 2 || stats.Count() != 1 {
		t.Fatalf("attempts=%d fetch=%d stats=%d", len(pctx.attempts), transport.Count(), stats.Count())
	}
	if firstBody.CloseCount() != 1 || secondBody.CloseCount() != 1 || lease.ReleaseCount() != 1 {
		t.Fatalf("body closes=(%d,%d) lease releases=%d", firstBody.CloseCount(), secondBody.CloseCount(), lease.ReleaseCount())
	}
	events.RequireOrder(t, "retry:reserve", "close:flagship", "retry:activate", "fetch:p1.example", "close:replacement")
}

type x3ExecutionConfig struct {
	providers         []*model.Provider
	selector          *x3Selector
	transport         HTTPTransport
	rules             *x3RuleProvider
	analyzer          *x3AnalyzerSpy
	health            *x3Health
	auth              ProviderAuthenticator
	stats             RuleStatistics
	backoff           BackoffWaiter
	globalMaxAttempts int
	capture           RequestCapture
	logger            *zap.Logger
}

func x3Execute(t *testing.T, config x3ExecutionConfig) (*httptest.ResponseRecorder, *proxyContext) {
	t.Helper()
	storeProviders := make([]model.Provider, 0, len(config.providers))
	for _, provider := range config.providers {
		if provider != nil {
			storeProviders = append(storeProviders, *provider)
		}
	}
	store := &x3Store{providers: storeProviders}
	logger := config.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := NewHandler(Config{
		Store: store, Selector: config.selector, Health: config.health, Auth: config.auth,
		RuleSetProvider: config.rules, ResponseAnalyzer: config.analyzer,
		RuleStatistics: config.stats, BackoffWaiter: config.backoff, Capture: config.capture, Logger: logger,
	})
	requestBody := []byte(`{"model":"x3-model"}`)
	request := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	pctx := &proxyContext{
		handler: handler, r: request, w: recorder,
		cfg: &runtimeConfig{
			globalAuthMode: DefaultGlobalAuthMode, globalMaxAttempts: config.globalMaxAttempts,
			readTimeout: time.Hour, sseIdleTimeout: time.Hour, stickyMode: model.StickyModeOff,
		},
		transport: config.transport, apiType: APITypeCodex, body: requestBody,
		info:      RequestInfo{Model: "x3-model", APIType: APITypeCodex, Path: "/responses", Method: http.MethodPost},
		selectReq: &model.SelectRequest{APIType: APITypeCodex, Model: "x3-model", StickyMode: model.StickyModeOff},
		startTime: time.Now(), requestID: x3TestRequestID, liveBytes: &LiveBytesTracker{},
		attempts: make([]model.RequestAttempt, 0),
	}
	pctx.capture = handler.beginGatewayCapture(pctx.requestID, pctx.startTime)
	pctx.captureParticipates = pctx.capture.Valid()
	handler.executeProxy(request.Context(), pctx)
	if pctx.captureParticipates {
		pctx.capture.Finish(requestcapture.GatewayOutcome{})
	}
	return recorder, pctx
}

func x3Provider(id string) *model.Provider {
	return &model.Provider{
		ID: id, Name: "provider-" + id, Enabled: true, APIKey: "secret", AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{ProviderID: id, APIType: APITypeCodex, BaseURL: "https://" + id + ".example"}},
	}
}

func x3RetryOnlyAction(t *testing.T, retries int) errorrule.Action {
	t.Helper()
	action, err := errorrule.NewRetryOnlyAction(retries, model.BackoffPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func x3RetryThenSwitchAction(t *testing.T, retries int) errorrule.Action {
	t.Helper()
	action, err := errorrule.NewRetryThenSwitchAction(retries, model.BackoffPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func x3CompiledRuleSet(t *testing.T, revision int64, action errorrule.Action, keyword string) *errorrule.CompiledRuleSet {
	t.Helper()
	generation, err := errorrule.ParseRuleGeneration("10000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	apiType := apicontract.APITypeCodex
	now := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	rule := errorrule.NewRule(errorrule.RuleSpec{
		Name: "X3 integration rule", Enabled: true, Target: errorrule.NewGlobalTarget(),
		APIType: &apiType, Keywords: []string{keyword}, MatchMode: errorrule.MatchAny, Action: action,
	}, errorrule.RuleMetadata{
		ID: "00000000-0000-4000-8000-000000000001", Generation: generation,
		Position: 0, CreatedAt: now, UpdatedAt: now,
	})
	compiled, err := errorrule.CompileRuleSet(errorrule.Revision(revision), []errorrule.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func x3AnalyzerSpyForTest(t *testing.T) *x3AnalyzerSpy {
	t.Helper()
	budget, err := responseanalysis.NewDefaultProcessMemoryBudget()
	if err != nil {
		t.Fatal(err)
	}
	analyzer, err := responseanalysis.NewAnalyzer(
		responseanalysis.NewRegistry(), budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &x3AnalyzerSpy{inner: analyzer}
}

type x3AnalyzerSpy struct {
	inner        *responseanalysis.Analyzer
	starts       atomic.Int32
	operationMu  sync.Mutex
	operationIDs []string
}

func (a *x3AnalyzerSpy) Start(ctx context.Context, input responseanalysis.StartInput) *responseanalysis.PendingResponse {
	a.starts.Add(1)
	a.operationMu.Lock()
	a.operationIDs = append(a.operationIDs, input.OperationID)
	a.operationMu.Unlock()
	return a.inner.Start(ctx, input)
}

func (a *x3AnalyzerSpy) Count() int { return int(a.starts.Load()) }

func (a *x3AnalyzerSpy) OperationIDs() []string {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	return append([]string(nil), a.operationIDs...)
}

type x3RuleProvider struct {
	mu      sync.RWMutex
	current *errorrule.CompiledRuleSet
	calls   atomic.Int32
}

func (p *x3RuleProvider) CurrentRuleSet() *errorrule.CompiledRuleSet {
	p.calls.Add(1)
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

func (p *x3RuleProvider) Set(snapshot *errorrule.CompiledRuleSet) {
	p.mu.Lock()
	p.current = snapshot
	p.mu.Unlock()
}

func (p *x3RuleProvider) Calls() int { return int(p.calls.Load()) }

type x3RuleStats struct{ hits atomic.Int32 }

func (s *x3RuleStats) Hit(statistics.Handle, time.Time) error {
	s.hits.Add(1)
	return nil
}

func (s *x3RuleStats) Count() int { return int(s.hits.Load()) }

type x3Backoff struct{ events *x3EventLog }

func (b x3Backoff) Wait(ctx context.Context, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.events.Add("backoff")
	return nil
}

type x3Auth struct {
	refresh   bool
	events    *x3EventLog
	applies   atomic.Int32
	refreshes atomic.Int32
}

func (a *x3Auth) ApplyProviderCredentials(
	ctx context.Context,
	header http.Header,
	_ *model.Provider,
	_, _ string,
	_ *http.Request,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.applies.Add(1)
	a.events.Add("auth:apply")
	header.Set("Authorization", "Bearer refreshed")
	return nil
}

func (a *x3Auth) RefreshProviderCredentials(ctx context.Context, _ *model.Provider) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	a.refreshes.Add(1)
	a.events.Add("auth:refresh")
	return a.refresh, nil
}

func (a *x3Auth) ApplyCount() int   { return int(a.applies.Load()) }
func (a *x3Auth) RefreshCount() int { return int(a.refreshes.Load()) }

type x3Store struct{ providers []model.Provider }

func (s *x3Store) ListProvidersByAPIType(context.Context, string) ([]model.Provider, error) {
	return append([]model.Provider(nil), s.providers...), nil
}

func (*x3Store) GetConfig(context.Context, string) (string, error)            { return "", nil }
func (*x3Store) InsertLog(context.Context, *model.RequestLog) error           { return nil }
func (*x3Store) InsertAttempts(context.Context, []model.RequestAttempt) error { return nil }

type x3Health struct {
	mu            sync.Mutex
	success       map[string]int
	failure       map[string]int
	openOnFailure bool
}

func newX3Health() *x3Health {
	return &x3Health{success: make(map[string]int), failure: make(map[string]int)}
}

func (h *x3Health) MarkSuccess(_ context.Context, providerID string) {
	h.mu.Lock()
	h.success[providerID]++
	h.mu.Unlock()
}

func (h *x3Health) MarkFailure(_ context.Context, providerID string, _ error) bool {
	h.mu.Lock()
	h.failure[providerID]++
	h.mu.Unlock()
	return h.openOnFailure
}

func (*x3Health) RecoverIfExpired(context.Context, string) bool                 { return false }
func (*x3Health) IsAvailable(context.Context, string) bool                      { return true }
func (*x3Health) SuspendUntil(context.Context, string, time.Time, string) error { return nil }
func (*x3Health) ManualDisable(context.Context, string, string) error           { return nil }
func (*x3Health) ManualEnable(context.Context, string) error                    { return nil }
func (*x3Health) ResetCircuitBreaker(string)                                    {}

func (h *x3Health) Successes(providerID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.success[providerID]
}

func (h *x3Health) Failures(providerID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failure[providerID]
}

type x3EventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *x3EventLog) Add(event string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *x3EventLog) Snapshot() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *x3EventLog) RequireOrder(t *testing.T, expected ...string) {
	t.Helper()
	events := l.Snapshot()
	cursor := 0
	for _, event := range events {
		if cursor < len(expected) && event == expected[cursor] {
			cursor++
		}
	}
	if cursor != len(expected) {
		t.Fatalf("events = %v; missing ordered suffix %v", events, expected[cursor:])
	}
}

type x3Lease struct {
	provider        *model.Provider
	events          *x3EventLog
	releaseObserver func()
	released        atomic.Bool
	releases        atomic.Int32
}

func x3NewLease(provider *model.Provider, events *x3EventLog) *x3Lease {
	return &x3Lease{provider: provider, events: events}
}

func (l *x3Lease) Provider() *model.Provider { return l.provider }
func (l *x3Lease) ProviderID() string {
	if l == nil || l.provider == nil {
		return ""
	}
	return l.provider.ID
}
func (*x3Lease) Generation() uint64 { return 1 }
func (l *x3Lease) CapabilityIdentity() uintptr {
	if l == nil {
		return 0
	}
	return reflect.ValueOf(l).Pointer()
}
func (l *x3Lease) Held() bool { return l != nil && !l.released.Load() }
func (l *x3Lease) Release() bool {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return false
	}
	if l.releaseObserver != nil {
		l.releaseObserver()
	}
	l.releases.Add(1)
	l.events.Add("lease:release:" + l.ProviderID())
	return true
}
func (l *x3Lease) ReleaseCount() int { return int(l.releases.Load()) }

type x3Selector struct {
	mu                  sync.Mutex
	initial             *model.Provider
	initialLease        *x3Lease
	alternate           *model.Provider
	events              *x3EventLog
	permit              *x3Permit
	reservation         *x3Reservation
	sameReserves        int
	dispatchReserveErr  error
	dispatchActivateErr error
}

func (*x3Selector) SelectWithMetadata(context.Context, *model.SelectRequest) (*selector.SelectResult, error) {
	return nil, errors.New("legacy provider selection is outside the HTTP lease contract")
}

func (*x3Selector) UpdateStickyWithTTL(*model.SelectRequest, string, time.Duration) {}
func (*x3Selector) EvictProviderContinuity(string)                                  {}
func (s *x3Selector) SelectInitial(ctx context.Context, request *model.SelectRequest) (*providerSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.events.Add("select:initial")
	if s.initial == nil || s.initialLease == nil {
		return nil, internal.ErrNoProvider
	}
	return &providerSelection{
		provider: s.initial, lease: s.initialLease,
		metadata: selector.BuildSelectionMetadata(request, selector.SelectionSourceStrategy),
	}, nil
}

func (*x3Selector) SelectActive(context.Context, *model.SelectRequest, providerLease) (*providerSelection, error) {
	return nil, internal.ErrNoProvider
}

func (s *x3Selector) ReserveSameProviderDispatch(
	ctx context.Context,
	current providerLease,
	_ *model.SelectRequest,
) (sameProviderDispatchPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if current == nil || !current.Held() || current.Provider() == nil {
		return nil, internal.ErrNoProvider
	}
	if s.dispatchReserveErr != nil {
		return nil, s.dispatchReserveErr
	}
	s.events.Add("dispatch:reserve")
	return &x3DispatchPermit{
		provider: current.Provider(), current: current, events: s.events,
		activateErr: s.dispatchActivateErr,
	}, nil
}

func (s *x3Selector) ReserveSameProviderRetry(ctx context.Context, input sameProviderRetryReservation) (retryPermit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.events.Add("retry:reserve")
	permit := &x3Permit{
		provider: input.current.Provider(), ledger: input.ledger, key: input.ruleKey,
		globalMaxAttempts: input.globalMaxAttempts, events: s.events,
	}
	s.mu.Lock()
	s.sameReserves++
	s.permit = permit
	s.mu.Unlock()
	return permit, nil
}

func (s *x3Selector) ReserveAlternate(
	ctx context.Context,
	request *model.SelectRequest,
	_ map[string]bool,
) (alternateProviderReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.events.Add("alternate:reserve")
	if s.alternate == nil {
		return nil, internal.ErrNoProvider
	}
	reservation := &x3Reservation{
		provider: s.alternate, lease: x3NewLease(s.alternate, s.events), events: s.events,
		metadata: selector.BuildSelectionMetadata(request, selector.SelectionSourceAlternate),
	}
	s.mu.Lock()
	s.reservation = reservation
	s.mu.Unlock()
	return reservation, nil
}

func (s *x3Selector) Permit() *x3Permit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permit
}

func (s *x3Selector) Reservation() *x3Reservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reservation
}

func (s *x3Selector) SameRetryReservations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sameReserves
}

type x3Permit struct {
	mu                sync.Mutex
	provider          *model.Provider
	ledger            errorrule.RetryLedger
	after             errorrule.RetryLedger
	key               errorrule.ProviderRuleKey
	globalMaxAttempts uint
	events            *x3EventLog
	used              bool
}

type x3DispatchPermit struct {
	mu          sync.Mutex
	provider    *model.Provider
	current     providerLease
	events      *x3EventLog
	activateErr error
	used        bool
}

func (p *x3DispatchPermit) Provider() *model.Provider { return p.provider }
func (p *x3DispatchPermit) Activate() (*model.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used || p.current == nil || !p.current.Held() {
		return nil, selector.ErrDispatchPermitReleased
	}
	p.used = true
	if p.activateErr != nil {
		p.events.Add("dispatch:reject")
		return nil, p.activateErr
	}
	p.events.Add("dispatch:activate")
	return p.provider, nil
}
func (p *x3DispatchPermit) Release() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used {
		return false
	}
	p.used = true
	p.events.Add("dispatch:release")
	return true
}

func (p *x3Permit) Provider() *model.Provider { return p.provider }
func (p *x3Permit) Activate() (errorrule.RetryLedger, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used {
		return errorrule.RetryLedger{}, errors.New("retry permit already consumed")
	}
	p.used = true
	p.events.Add("retry:activate")
	next, err := p.ledger.StartRuleRetry(p.key, p.globalMaxAttempts)
	p.after = next
	return next, err
}
func (p *x3Permit) Release() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used {
		return false
	}
	p.used = true
	p.events.Add("retry:release")
	return true
}
func (p *x3Permit) LedgerBefore() errorrule.RetryLedger { return p.ledger }
func (p *x3Permit) LedgerAfter() errorrule.RetryLedger {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.after
}

type x3Reservation struct {
	mu        sync.Mutex
	provider  *model.Provider
	lease     *x3Lease
	metadata  selector.SelectionMetadata
	events    *x3EventLog
	prepared  bool
	activated int
	released  int
}

func (r *x3Reservation) Provider() *model.Provider            { return r.provider }
func (r *x3Reservation) Metadata() selector.SelectionMetadata { return r.metadata }
func (r *x3Reservation) PrepareActivation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events.Add("alternate:prepare")
	if r.released > 0 {
		return internal.ErrNoProvider
	}
	r.prepared = true
	return nil
}
func (r *x3Reservation) Activate() providerLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.prepared || r.released > 0 || r.activated > 0 {
		return nil
	}
	r.activated++
	r.events.Add("alternate:activate")
	return r.lease
}
func (r *x3Reservation) Release() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released > 0 || r.activated > 0 {
		return false
	}
	r.released++
	r.events.Add("alternate:release")
	return r.lease.Release()
}
func (r *x3Reservation) Lease() *x3Lease { return r.lease }
func (r *x3Reservation) ActivationCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activated
}
func (r *x3Reservation) ReleaseCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.released
}
