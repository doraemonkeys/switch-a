package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCodexDialDisclosureSettlesProvisionalOwnership(t *testing.T) {
	for _, disclosure := range []upstreamtransport.RequestDisclosure{
		upstreamtransport.RequestDisclosureNone,
		upstreamtransport.RequestDisclosurePossible,
		upstreamtransport.RequestDisclosureConfirmed,
		upstreamtransport.RequestDisclosureUnknown,
	} {
		t.Run(disclosure.String(), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
			authorizeCodexRequest(request)
			request.Header.Set("Thread-Id", "unbound-thread")
			operation, err := testCodexRuntime(t).Begin(t.Context(), request, APITypeCodex, "dial-ownership", "")
			if err != nil {
				t.Fatal(err)
			}
			core, logs := observer.New(zap.DebugLevel)
			orchestrator := &WebSocketSessionOrchestrator{
				codexOperation: operation, handler: &Gateway{logger: zap.New(core)}, requestID: "dial-ownership",
			}
			first := disclosurePreparedDial(t, "first")
			if err := orchestrator.prepareCodexPhysicalDial(t.Context(), &first); err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.finishCodexPhysicalDial(t.Context(), first, DialExchange{Disclosure: disclosure}); err != nil {
				t.Fatal(err)
			}
			second := disclosurePreparedDial(t, "second")
			err = orchestrator.prepareCodexPhysicalDial(t.Context(), &second)
			if disclosure.DefinitelyNotDisclosed() {
				if err != nil {
					t.Fatalf("undisclosed attempt left ownership behind: %v", err)
				}
				if authority, _ := operation.RequiredAuthority(); authority != nil {
					t.Fatal("replacement was pinned before transmission")
				}
			} else if codexws.Classify(err) != codexws.FailureIdentity {
				t.Fatalf("disclosure=%s allowed cross-account dial: %v", disclosure, err)
			}
			entries := logs.FilterMessage("websocket.dial_ownership_settled").All()
			if len(entries) != 1 {
				t.Fatalf("settlement logs=%d, want one", len(entries))
			}
			fields := entries[0].ContextMap()
			if fields["operation_id"] != "dial-ownership" || fields["provider_id"] != "first" ||
				fields["request_disclosure"] != disclosure.String() {
				t.Fatalf("settlement evidence=%v", fields)
			}
		})
	}
}

func TestUndisclosedDialPreservesPreviouslyCommittedThreadOwner(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	authorizeCodexRequest(request)
	request.Header.Set("Thread-Id", "existing-thread")
	runtime := testCodexRuntime(t)
	for _, disclosure := range []upstreamtransport.RequestDisclosure{
		upstreamtransport.RequestDisclosureConfirmed, upstreamtransport.RequestDisclosureNone,
	} {
		operation, err := runtime.Begin(t.Context(), request, APITypeCodex, disclosure.String(), "")
		if err != nil {
			t.Fatal(err)
		}
		orchestrator := &WebSocketSessionOrchestrator{codexOperation: operation, handler: &Gateway{logger: zap.NewNop()}}
		first := disclosurePreparedDial(t, "first")
		if err := orchestrator.prepareCodexPhysicalDial(t.Context(), &first); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.finishCodexPhysicalDial(t.Context(), first, DialExchange{Disclosure: disclosure}); err != nil {
			t.Fatal(err)
		}
		second := disclosurePreparedDial(t, "second")
		if err := orchestrator.prepareCodexPhysicalDial(t.Context(), &second); codexws.Classify(err) != codexws.FailureIdentity {
			t.Fatalf("existing owner lost after %s dial: %v", disclosure, err)
		}
	}
}

func disclosurePreparedDial(t *testing.T, providerID string) webSocketPreparedProviderAttempt {
	t.Helper()
	provider := &model.Provider{
		ID:                 providerID,
		CredentialSessions: testCredentialSessions(providerID, APITypeCodex, credentialsession.KindAPIKey, providerID+"-secret"),
	}
	prepared := testPreparedProviderAttempt(t, provider, APITypeCodex, "https://"+providerID+".example.test/responses")
	subject, err := codexidentity.CredentialSubjectFromSession(prepared.credential.Subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.applied, err = codexidentity.AppliedIdentityFromRequest(prepared.candidate.Authority().Vendor(), prepared.finalURL, subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.headers = make(http.Header)
	return prepared
}

func TestDialDisclosureDoesNotTrustUnobservedCustomDialer(t *testing.T) {
	wantErr := errors.New("dialer did not report a transmission")
	for _, status := range []int{0, http.StatusServiceUnavailable} {
		forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{
			Logger: zap.NewNop(),
			Dialer: &mockDialer{dialFunc: func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
				if status != 0 {
					return nil, &http.Response{StatusCode: status}, wantErr
				}
				return nil, nil, wantErr
			}},
		})
		exchange := forwarder.dialUpstream(t.Context(), WebSocketDialRequest{URL: "http://upstream.test/responses"})
		want := upstreamtransport.RequestDisclosureUnknown
		if status != 0 {
			want = upstreamtransport.RequestDisclosureConfirmed
		}
		if exchange.Disclosure != want || !errors.Is(exchange.Err, wantErr) {
			t.Fatalf("status=%d disclosure=%s err=%v", status, exchange.Disclosure, exchange.Err)
		}
	}
}
