package websocketproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
	wsdisguise "github.com/doraemonkeys/switch-a/internal/websocketproxy/disguise"
	"go.uber.org/zap"
)

type testDisguiseRepository struct {
	mu              sync.Mutex
	mapped          map[clientdisguise.MappingKey]string
	commits         []string
	excludedSession string
	mappingError    error
	revision        string
}

func (r *testDisguiseRepository) EvaluateCandidate(_ context.Context, id string, basis clientdisguise.AccountBasis, policy clientdisguise.Policy, facts clientdisguise.PlatformFacts) (clientdisguise.Candidate, error) {
	return clientdisguise.Candidate{CredentialSessionID: id, AccountBasis: basis, Policy: policy, Facts: facts,
		Profile:  clientdisguise.ProfileRevision{ID: r.revision, ClientVersion: "1.0.0", Tuple: clientdisguise.Tuple{ClientType: "cli", Platform: "windows", Arch: "amd64"}, Features: clientdisguise.Features{UserAgent: "profile-" + r.revision}},
		Decision: clientdisguise.PlatformDecision{Allowed: true, Facts: facts}}, nil
}
func (r *testDisguiseRepository) CommitTarget(_ context.Context, c clientdisguise.Candidate) (clientdisguise.TargetSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits = append(r.commits, c.CredentialSessionID)
	if c.CredentialSessionID == r.excludedSession {
		return clientdisguise.TargetSnapshot{}, clientdisguise.ErrCandidateExcluded
	}
	return clientdisguise.TargetSnapshot{Policy: c.Policy, Profile: c.Profile, Login: clientdisguise.LoginIdentity{
		CredentialSessionID: c.CredentialSessionID, GenerationID: "generation-" + c.CredentialSessionID, DeviceID: "device-" + c.CredentialSessionID, AccountBasis: c.AccountBasis}}, nil
}
func (r *testDisguiseRepository) MapIdentity(_ context.Context, key clientdisguise.MappingKey) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mappingError != nil {
		return "", r.mappingError
	}
	if r.mapped == nil {
		r.mapped = make(map[clientdisguise.MappingKey]string)
	}
	if mapped, ok := r.mapped[key]; ok {
		return mapped, nil
	}
	mapped := key.GenerationID + "-" + key.Namespace + "-" + key.Original
	r.mapped[key] = mapped
	return mapped, nil
}
func (r *testDisguiseRepository) RestoreIdentity(_ context.Context, generation, client, namespace, mapped string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mappingError != nil {
		return "", false, r.mappingError
	}
	for key, value := range r.mapped {
		if key.GenerationID == generation && key.ClientIdentityID == client && key.Namespace == namespace && value == mapped {
			return key.Original, true, nil
		}
	}
	return mapped, false, nil
}
func testDisguiseProvider(id string) model.Provider {
	return model.Provider{ID: id, Enabled: true, ClientDisguise: clientdisguise.Policy{Enabled: true},
		APITypes:           []model.ProviderAPIType{{ProviderID: id, APIType: APITypeCodex, BaseURL: "https://upstream.example"}},
		CredentialSessions: testCredentialSessions(id, APITypeCodex, credentialsession.KindAPIKey, "secret")}
}
func newDisguiseTestOrchestrator(t *testing.T, repository *testDisguiseRepository, providers []model.Provider) *WebSocketSessionOrchestrator {
	t.Helper()
	session, err := wsdisguise.New(context.Background(), repository, providers, http.Header{"User-Agent": {"codex/1.0.0 (Windows; amd64)"}}, "client", "disguise-operation", nil)
	if err != nil {
		t.Fatal(err)
	}
	operation := testCodexOperation(t)
	request := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5", ClientDisguise: session.Operation()}
	return newWebSocketSessionOrchestrator(&Gateway{logger: zap.NewNop()}, webSocketSessionOrchestratorConfig{
		apiType: APITypeCodex, requestID: "disguise-operation", selectReq: request, codexOperation: operation, disguise: session})
}
func selectDisguiseTestTarget(t *testing.T, o *WebSocketSessionOrchestrator, provider *model.Provider) {
	t.Helper()
	if _, err := o.disguise.Operation().Commit(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	if err := o.disguise.Select(provider); err != nil {
		t.Fatal(err)
	}
	prepared := testPreparedProviderAttempt(t, provider, APITypeCodex, "https://upstream.example/responses")
	subject, err := codexidentity.CredentialSubjectFromSession(prepared.credential.Subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.applied, err = codexidentity.AppliedIdentityFromRequest(prepared.candidate.Authority().Vendor(), prepared.finalURL, subject)
	if err != nil {
		t.Fatal(err)
	}
	if err = o.prepareCodexPhysicalDial(context.Background(), &prepared); err != nil {
		t.Fatal(err)
	}
	o.currentProvider = provider
}
func TestDisguiseReplayDerivesEachTargetFromOriginalPermit(t *testing.T) {
	repository := &testDisguiseRepository{revision: "revision-one"}
	providers := []model.Provider{testDisguiseProvider("first"), testDisguiseProvider("second")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	selectDisguiseTestTarget(t, o, &providers[0])
	source := []byte(`{"type":"response.create","turn_id":"turn","installation_id":"client-device","input":[{"text":"turn must remain unchanged"}]}`)
	original := append([]byte(nil), source...)
	decision := o.codexClientPreWrite(context.Background())(webSocketPreWriteContext{MessageType: websocket.MessageText, Data: source})
	if decision.Action != webSocketPreWriteActionForward {
		t.Fatal(decision.Err)
	}
	if bytes.Equal(decision.physicalPayload(source), source) || !bytes.Equal(source, original) {
		t.Fatalf("first derivation changed source or was absent: %s", decision.physicalPayload(source))
	}
	stored := decision.forReplayStorage()
	if stored.PreparedPayload != nil || stored.OnWriteConfirmed != nil {
		t.Fatal("target-specific delivery retained for replay")
	}
	repository.revision = "revision-two"
	selectDisguiseTestTarget(t, o, &providers[1])
	replay := stored.PrepareReplay(original)
	if replay.Action != webSocketPreWriteActionForward {
		t.Fatal(replay.Err)
	}
	if bytes.Equal(replay.physicalPayload(original), decision.physicalPayload(original)) {
		t.Fatal("different login reused previous target bytes")
	}
	if !bytes.Contains(replay.PreparedPayload, []byte("device-second")) || bytes.Contains(replay.PreparedPayload, []byte("device-first")) {
		t.Fatalf("replay target leaked: %s", replay.PreparedPayload)
	}
	if !bytes.Contains(replay.PreparedPayload, []byte("turn must remain unchanged")) {
		t.Fatal("business payload transformed")
	}
	headers, err := o.disguise.Current().Headers(context.Background(), http.Header{})
	if err != nil || headers.Get("User-Agent") != "profile-revision-one" {
		t.Fatalf("operation revision changed: %v %v", headers, err)
	}
	if client, err := o.disguise.HTTPClient(); err != nil || client != nil {
		t.Fatalf("application profile changed default transport: %v %v", client, err)
	}
	fresh := newDisguiseTestOrchestrator(t, repository, providers)
	selectDisguiseTestTarget(t, fresh, &providers[1])
	headers, err = fresh.disguise.Current().Headers(context.Background(), http.Header{})
	if err != nil || headers.Get("User-Agent") != "profile-revision-two" {
		t.Fatalf("reconnect did not take current revision: %v %v", headers, err)
	}
}
func TestDisguiseConversionFailureIsTerminalAndHasDurableEvidence(t *testing.T) {
	repository := &testDisguiseRepository{revision: "one"}
	providers := []model.Provider{testDisguiseProvider("first")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	selectDisguiseTestTarget(t, o, &providers[0])
	repository.mappingError = errors.New("mapping database unavailable")
	decision := o.codexClientPreWrite(context.Background())(webSocketPreWriteContext{MessageType: websocket.MessageText, Data: []byte(`{"type":"response.create","turn_id":"turn"}`)})
	if decision.Action != webSocketPreWriteActionReject {
		t.Fatalf("failed conversion allowed: %#v", decision)
	}
	failure := disguiseFailure(decision.Err)
	if failure == nil || failure.DiagnosticID == "" {
		t.Fatalf("missing diagnostic: %v", decision.Err)
	}
	attempt := WebSocketAttemptResult{Provider: &providers[0], ForwardErr: decision.Err, Result: &WebSocketResult{Err: decision.Err, TerminalCause: model.TerminalUpstreamTransportError, CompletionObserved: true}}
	o.finishDisguiseAttempt(&attempt)
	if attempt.Result.TerminalCause != model.TerminalInternalError || o.shouldSwitchProvider(attempt) || o.shouldFallbackToSuppressedPayload(attempt) {
		t.Fatalf("conversion retry allowed: %#v", attempt)
	}
	if health := assessWebSocketHealth(&providers[0], attempt.Result); health.markFailure || health.markSuccess {
		t.Fatalf("conversion affected provider health: %#v", health)
	}
	encoded := buildWebSocketAttemptEvidence(attempt)
	if encoded == nil || !strings.Contains(*encoded, failure.DiagnosticID) || !strings.Contains(*encoded, "mapping database unavailable") {
		t.Fatalf("missing durable evidence: %v", encoded)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*encoded), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope["client_disguise"]) == 0 {
		t.Fatal("client_disguise envelope absent")
	}
	var closeError websocket.CloseError
	if !errors.As(decision.Err, &closeError) || closeError.Code != websocket.StatusInternalError || !strings.Contains(closeError.Reason, failure.DiagnosticID) {
		t.Fatalf("client close lost diagnostic: %v", decision.Err)
	}
}
func TestDisguiseFaultOutranksSiblingCancellationAndSuppression(t *testing.T) {
	failure := &wire.Failure{DiagnosticID: "diagnostic", Cause: errors.New("conversion")}
	first := webSocketRelayResult{err: context.Canceled, errorOrder: 1}
	second := webSocketRelayResult{err: fmt.Errorf("physical delivery: %w", failure), errorOrder: 2}
	result := reduceWebSocketRelayErrors(first, second)
	if result.terminalCause != model.TerminalInternalError || !errors.Is(result.err, failure) {
		t.Fatalf("lost transformation error: %#v", result)
	}
	first.suppressedUpstreamError = &WebSocketUpstreamError{}
	if firstSuppressedUpstreamError(first, second) != nil {
		t.Fatal("suppression bypassed conversion fault")
	}
}
func TestDisguiseWarmupAndOpaquePayloadsPreserveMeaning(t *testing.T) {
	repository := &testDisguiseRepository{revision: "one"}
	providers := []model.Provider{testDisguiseProvider("first")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	selectDisguiseTestTarget(t, o, &providers[0])
	for _, source := range [][]byte{[]byte(`{"type":"response.create","turn_id":""}`), []byte(`{"type":"future.event","turn_id":"opaque"}`), []byte("opaque")} {
		decision := o.codexClientPreWrite(context.Background())(webSocketPreWriteContext{MessageType: websocket.MessageText, Data: source})
		if decision.Action != webSocketPreWriteActionForward || !bytes.Equal(decision.physicalPayload(source), source) {
			t.Fatalf("legal frame changed or rejected: %s %v", source, decision.Err)
		}
	}
}
