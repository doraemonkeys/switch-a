package proxy

import (
	"testing"

	"github.com/coder/websocket"
)

func TestCodexWebSocketMessageObserver_CapturesUpstreamErrorWithNestedErrorType(t *testing.T) {
	t.Parallel()

	observer := newCodexWebSocketMessageObserver("gpt-5.4", nil, nil, nil)
	payload := []byte(`{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed","code":"model_not_allowed"},"status":403,"type":"error"}`)

	observer.ObserveUpstreamMessage(websocket.MessageText, payload)

	observation := observer.Snapshot()
	if observation.UpstreamError == nil {
		t.Fatal("expected UpstreamError to be captured")
	}
	if observation.UpstreamError.EventType != "model_not_allowed" {
		t.Fatalf("EventType = %q, want %q", observation.UpstreamError.EventType, "model_not_allowed")
	}
	if observation.UpstreamError.StatusCode != 403 {
		t.Fatalf("StatusCode = %d, want 403", observation.UpstreamError.StatusCode)
	}
	if observation.UpstreamError.Code != "model_not_allowed" {
		t.Fatalf("Code = %q, want %q", observation.UpstreamError.Code, "model_not_allowed")
	}
	if observation.UpstreamError.Message != "Model 'gpt-5.4' is not allowed" {
		t.Fatalf("Message = %q, want %q", observation.UpstreamError.Message, "Model 'gpt-5.4' is not allowed")
	}
	if observation.UpstreamError.Raw != string(payload) {
		t.Fatalf("Raw = %q, want payload", observation.UpstreamError.Raw)
	}
	if observation.SessionCommitted {
		t.Fatal("semantic error must not mark the session committed")
	}
}

func TestCodexWebSocketMessageObserver_CommitsOnResponseCreated(t *testing.T) {
	t.Parallel()

	var updates []WebSocketObservation
	var commits []WebSocketObservation
	observer := newCodexWebSocketMessageObserver(
		ModelUnknown,
		nil,
		func(observation WebSocketObservation) {
			updates = append(updates, observation)
		},
		func(observation WebSocketObservation) {
			commits = append(commits, observation)
		},
	)

	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created","session":{"model":"gpt-5.4"}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4"}}`))

	observation := observer.Snapshot()
	if !observation.SessionCommitted {
		t.Fatal("expected SessionCommitted=true after response.created")
	}
	if observation.CommitEventType != webSocketEventResponseCreated {
		t.Fatalf("CommitEventType = %q, want %q", observation.CommitEventType, webSocketEventResponseCreated)
	}
	if observation.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want %q", observation.Model, "gpt-5.4")
	}
	if observation.ParseDegraded {
		t.Fatal("ParseDegraded must stay false for valid semantic frames")
	}
	if len(commits) != 1 {
		t.Fatalf("commit callback count = %d, want 1", len(commits))
	}
	if !commits[0].SessionCommitted {
		t.Fatal("commit callback must observe committed state")
	}
	if len(updates) == 0 {
		t.Fatal("expected onUpdate to publish semantic changes")
	}
}

func TestCodexWebSocketMessageObserver_ParseDegradedOnInvalidUpstreamJSON(t *testing.T) {
	t.Parallel()

	observer := newCodexWebSocketMessageObserver("gpt-5.4", nil, nil, nil)

	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.created"`))

	observation := observer.Snapshot()
	if !observation.ParseDegraded {
		t.Fatal("expected ParseDegraded=true after invalid upstream JSON")
	}
	if !observer.ParseDegraded() {
		t.Fatal("ParseDegraded accessor must mirror snapshot state")
	}
	if observation.SessionCommitted {
		t.Fatal("parse degradation alone must not mark the session committed")
	}
}

func TestClassifyWebSocketUpstreamError_StatusAndIdentifierSignals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *WebSocketUpstreamError
		want webSocketSemanticClassification
	}{
		{
			name: "provider scoped from allowlisted status",
			err: &WebSocketUpstreamError{
				EventType:  "error",
				StatusCode: 429,
				Message:    "provider overloaded",
			},
			want: webSocketSemanticClassificationProviderScopedAllowlisted,
		},
		{
			name: "provider scoped from allowlisted identifier",
			err: &WebSocketUpstreamError{
				EventType: "error",
				Code:      "insufficient_quota",
				Message:   "quota exhausted",
			},
			want: webSocketSemanticClassificationProviderScopedAllowlisted,
		},
		{
			name: "client scoped from request status",
			err: &WebSocketUpstreamError{
				EventType:  "invalid_request_error",
				StatusCode: 400,
				Message:    "bad request",
			},
			want: webSocketSemanticClassificationClientScoped,
		},
		{
			name: "conflicting status and identifier downgrade to unknown",
			err: &WebSocketUpstreamError{
				EventType:  "invalid_api_key",
				StatusCode: 400,
				Message:    "conflicting provider and client scope",
			},
			want: webSocketSemanticClassificationUnknown,
		},
		{
			name: "missing signals stays unknown",
			err: &WebSocketUpstreamError{
				EventType: "error",
				Message:   "unclassified",
			},
			want: webSocketSemanticClassificationUnknown,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyWebSocketUpstreamError(tt.err); got != tt.want {
				t.Fatalf("classifyWebSocketUpstreamError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideWebSocketUpstreamMessage_UsesClientVisibilityAndParseDegradation(t *testing.T) {
	t.Parallel()

	providerPayload := []byte(`{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed"},"status":403,"type":"error"}`)
	clientPayload := []byte(`{"error":{"message":"invalid client payload","type":"invalid_request_error"},"status":400,"type":"error"}`)

	tests := []struct {
		name          string
		payload       []byte
		parseDegraded bool
		clientVisible bool
		wantClass     webSocketSemanticClassification
		wantDecision  webSocketSemanticFrameDecision
	}{
		{
			name:         "suppress allowlisted provider scoped before visible",
			payload:      providerPayload,
			wantClass:    webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision: webSocketSemanticFrameDecisionSuppress,
		},
		{
			name:          "forward allowlisted provider scoped after visible",
			payload:       providerPayload,
			clientVisible: true,
			wantClass:     webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
		{
			name:         "forward client scoped before visible",
			payload:      clientPayload,
			wantClass:    webSocketSemanticClassificationClientScoped,
			wantDecision: webSocketSemanticFrameDecisionForward,
		},
		{
			name:          "parse degradation downgrades to unknown and forwards",
			payload:       providerPayload,
			parseDegraded: true,
			wantClass:     webSocketSemanticClassificationUnknown,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := decideWebSocketUpstreamMessage(websocket.MessageText, tt.payload, tt.parseDegraded, tt.clientVisible)
			if decision.Classification != tt.wantClass {
				t.Fatalf("Classification = %v, want %v", decision.Classification, tt.wantClass)
			}
			if decision.FrameDecision != tt.wantDecision {
				t.Fatalf("FrameDecision = %v, want %v", decision.FrameDecision, tt.wantDecision)
			}
		})
	}
}

func TestQuickExtractEventType_BoundedScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "extracts type near start of payload",
			data: []byte(`{"type":"response.created","response":{"id":"resp_123"}}`),
			want: webSocketEventResponseCreated,
		},
		{
			name: "returns empty when type lands beyond bounded scan window",
			data: append(make([]byte, typeFieldScanLimit), []byte(`"type":"response.created"`)...),
			want: "",
		},
		{
			name: "returns empty for unterminated value",
			data: []byte(`{"type":"response.created`),
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := quickExtractEventType(tt.data); got != tt.want {
				t.Fatalf("quickExtractEventType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPayloadMayContainError_UsesBoundedScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "detects nested error field near front",
			data: []byte(`{"type":"error","error":{"type":"auth_error"}}`),
			want: true,
		},
		{
			name: "ignores payloads without error field",
			data: []byte(`{"type":"session.updated"}`),
			want: false,
		},
		{
			name: "bounded scan ignores far-away error field",
			data: append(make([]byte, errorFieldScanLimit), []byte(`"error":{"type":"auth_error"}`)...),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payloadMayContainError(tt.data); got != tt.want {
				t.Fatalf("payloadMayContainError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldFastSkipCodexPayload_UsesEventTypeHeuristic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "high-volume audio append fast-skips",
			data: []byte(`{"type":"input_audio_buffer.append","audio":"Zm9v"}`),
			want: true,
		},
		{
			name: "observable response event does not fast-skip",
			data: []byte(`{"type":"response.created","response":{"id":"resp_123"}}`),
			want: false,
		},
		{
			name: "error payload stays observable",
			data: []byte(`{"type":"error","error":{"type":"auth_error"}}`),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldFastSkipCodexPayload(tt.data); got != tt.want {
				t.Fatalf("shouldFastSkipCodexPayload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildWebSocketUpstreamError_PrefersNestedErrorFieldsAndFallbacks(t *testing.T) {
	t.Parallel()

	data := []byte(`{"type":"error","status":403,"error":{"type":" auth_error ","code":" invalid_api_key ","message":" denied "}}`)
	event := &codexWebSocketEventEnvelope{
		Type:   "error",
		Status: 403,
		Error: &codexWebSocketEventError{
			Type:    " auth_error ",
			Code:    " invalid_api_key ",
			Message: " denied ",
		},
	}

	upstreamErr := buildWebSocketUpstreamError(event, data)
	if upstreamErr == nil {
		t.Fatal("expected upstream error")
	}
	if upstreamErr.EventType != " auth_error " {
		t.Fatalf("EventType = %q, want nested event type", upstreamErr.EventType)
	}
	if upstreamErr.Code != "invalid_api_key" {
		t.Fatalf("Code = %q, want trimmed code", upstreamErr.Code)
	}
	if upstreamErr.Message != "denied" {
		t.Fatalf("Message = %q, want trimmed message", upstreamErr.Message)
	}
	if upstreamErr.Raw != string(data) {
		t.Fatalf("Raw = %q, want original payload", upstreamErr.Raw)
	}

	fallback := buildWebSocketUpstreamError(&codexWebSocketEventEnvelope{
		Type: "error",
	}, []byte(`{"type":"error"}`))
	if fallback == nil {
		t.Fatal("expected fallback upstream error")
	}
	if fallback.Message != "error" {
		t.Fatalf("fallback Message = %q, want event type fallback", fallback.Message)
	}
}

func TestNormalizeAndClassifyWebSocketSemanticErrorKey(t *testing.T) {
	t.Parallel()

	if got := normalizeWebSocketSemanticErrorKey("  Invalid API Key  "); got != "invalid_api_key" {
		t.Fatalf("normalizeWebSocketSemanticErrorKey() = %q, want %q", got, "invalid_api_key")
	}

	tests := []struct {
		name string
		key  string
		want webSocketSemanticClassification
		ok   bool
	}{
		{
			name: "allowlisted provider key",
			key:  "invalid_api_key",
			want: webSocketSemanticClassificationProviderScopedAllowlisted,
			ok:   true,
		},
		{
			name: "client-scoped key",
			key:  "invalid_request_error",
			want: webSocketSemanticClassificationClientScoped,
			ok:   true,
		},
		{
			name: "unknown key",
			key:  "mystery_error",
			want: webSocketSemanticClassificationUnknown,
			ok:   false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := classifyWebSocketSemanticErrorKey(tt.key)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("classifyWebSocketSemanticErrorKey() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDecideWebSocketUpstreamError_UsesVisibilityAndParseDegradation(t *testing.T) {
	t.Parallel()

	upstreamErr := &WebSocketUpstreamError{
		EventType:  "auth_error",
		Code:       "invalid_api_key",
		StatusCode: 401,
	}

	tests := []struct {
		name          string
		parseDegraded bool
		clientVisible bool
		wantClass     webSocketSemanticClassification
		wantDecision  webSocketSemanticFrameDecision
	}{
		{
			name:         "suppresses allowlisted provider error before visible",
			wantClass:    webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision: webSocketSemanticFrameDecisionSuppress,
		},
		{
			name:          "forwards allowlisted provider error after visible",
			clientVisible: true,
			wantClass:     webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
		{
			name:          "parse degradation downgrades to unknown",
			parseDegraded: true,
			wantClass:     webSocketSemanticClassificationUnknown,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decision := decideWebSocketUpstreamError(upstreamErr, tt.parseDegraded, tt.clientVisible)
			if decision.Classification != tt.wantClass {
				t.Fatalf("Classification = %v, want %v", decision.Classification, tt.wantClass)
			}
			if decision.FrameDecision != tt.wantDecision {
				t.Fatalf("FrameDecision = %v, want %v", decision.FrameDecision, tt.wantDecision)
			}
		})
	}
}

func TestNewWebSocketMessageObserver_OnlyCodexReturnsSemanticObserver(t *testing.T) {
	t.Parallel()

	if observer := newWebSocketMessageObserver("claude", ModelUnknown, nil, nil, nil); observer != nil {
		t.Fatalf("newWebSocketMessageObserver(non-codex) = %T, want nil", observer)
	}
	if observer := newWebSocketMessageObserver(APITypeCodex, ModelUnknown, nil, nil, nil); observer == nil {
		t.Fatal("newWebSocketMessageObserver(codex) = nil, want semantic observer")
	}
}

func TestBuildWebSocketUpstreamError_FallsBackToCodeOrEventType(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"type":"error"}`)
	tests := []struct {
		name  string
		event *codexWebSocketEventEnvelope
		want  *WebSocketUpstreamError
	}{
		{
			name:  "nil event returns nil",
			event: nil,
			want:  nil,
		},
		{
			name: "blank message falls back to trimmed code",
			event: &codexWebSocketEventEnvelope{
				Type:   "error",
				Status: 401,
				Error: &codexWebSocketEventError{
					Code:    " invalid_api_key ",
					Message: "   ",
				},
			},
			want: &WebSocketUpstreamError{
				EventType:  "error",
				Code:       "invalid_api_key",
				StatusCode: 401,
				Message:    "invalid_api_key",
				Raw:        string(payload),
			},
		},
		{
			name: "missing error object falls back to event type",
			event: &codexWebSocketEventEnvelope{
				Type:   "auth_error",
				Status: 403,
			},
			want: &WebSocketUpstreamError{
				EventType:  "auth_error",
				StatusCode: 403,
				Message:    "auth_error",
				Raw:        string(payload),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := buildWebSocketUpstreamError(tt.event, payload); !upstreamErrorsEqual(got, tt.want) {
				t.Fatalf("buildWebSocketUpstreamError() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestClassifyWebSocketUpstreamErrorIdentifiers_NormalizesKeys(t *testing.T) {
	t.Parallel()

	got, matched := classifyWebSocketUpstreamErrorIdentifiers(&WebSocketUpstreamError{
		Code: " Rate Limit Error ",
	})
	if !matched {
		t.Fatal("matched = false, want true")
	}
	if got != webSocketSemanticClassificationProviderScopedAllowlisted {
		t.Fatalf("classification = %v, want %v", got, webSocketSemanticClassificationProviderScopedAllowlisted)
	}
}

func TestShouldFastSkipCodexPayload_PreservesPotentialErrorFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "transport-only event fast-skips",
			data: []byte(`{"type":"input_audio_buffer.append","delta":"abc"}`),
			want: true,
		},
		{
			name: "nested error signal disables fast skip",
			data: []byte(`{"type":"input_audio_buffer.append","error":{"type":"model_not_allowed"}}`),
			want: false,
		},
		{
			name: "observable event stays on semantic path",
			data: []byte(`{"type":"response.created","response":{"id":"resp_1"}}`),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldFastSkipCodexPayload(tt.data); got != tt.want {
				t.Fatalf("shouldFastSkipCodexPayload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideWebSocketUpstreamError_UsesClassificationAndParseDegradation(t *testing.T) {
	t.Parallel()

	providerScoped := &WebSocketUpstreamError{
		EventType: "auth_error",
		Code:      "model_not_allowed",
		Message:   "model access denied",
	}
	clientScoped := &WebSocketUpstreamError{
		EventType:  "invalid_request_error",
		Message:    "bad request",
		StatusCode: 400,
	}

	tests := []struct {
		name          string
		err           *WebSocketUpstreamError
		parseDegraded bool
		clientVisible bool
		wantClass     webSocketSemanticClassification
		wantDecision  webSocketSemanticFrameDecision
	}{
		{
			name:         "provider-scoped error suppresses before client-visible boundary",
			err:          providerScoped,
			wantClass:    webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision: webSocketSemanticFrameDecisionSuppress,
		},
		{
			name:          "provider-scoped error forwards once client already saw upstream data",
			err:           providerScoped,
			clientVisible: true,
			wantClass:     webSocketSemanticClassificationProviderScopedAllowlisted,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
		{
			name:         "client-scoped error always forwards",
			err:          clientScoped,
			wantClass:    webSocketSemanticClassificationClientScoped,
			wantDecision: webSocketSemanticFrameDecisionForward,
		},
		{
			name:          "parse degradation disables semantic failover",
			err:           providerScoped,
			parseDegraded: true,
			wantClass:     webSocketSemanticClassificationUnknown,
			wantDecision:  webSocketSemanticFrameDecisionForward,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := decideWebSocketUpstreamError(tt.err, tt.parseDegraded, tt.clientVisible)
			if decision.Classification != tt.wantClass {
				t.Fatalf("Classification = %v, want %v", decision.Classification, tt.wantClass)
			}
			if decision.FrameDecision != tt.wantDecision {
				t.Fatalf("FrameDecision = %v, want %v", decision.FrameDecision, tt.wantDecision)
			}
		})
	}
}

func TestCodexWebSocketSemantics_HelperBranches(t *testing.T) {
	t.Parallel()

	if got := (&WebSocketUpstreamError{StatusCode: 403}).IsAllowlistedProviderScoped(); !got {
		t.Fatal("IsAllowlistedProviderScoped() = false, want true for 403")
	}
	if got := shouldSkipCodexObservedPayload(websocket.MessageBinary, []byte(`{"type":"response.created"}`)); !got {
		t.Fatal("shouldSkipCodexObservedPayload(binary) = false, want true")
	}
	if got := shouldSkipCodexObservedPayload(websocket.MessageText, nil); !got {
		t.Fatal("shouldSkipCodexObservedPayload(empty) = false, want true")
	}
	if got := codexEventRepresentsError(nil); got {
		t.Fatal("codexEventRepresentsError(nil) = true, want false")
	}
	if got := codexEventRepresentsError(&codexWebSocketEventEnvelope{Status: 500}); !got {
		t.Fatal("codexEventRepresentsError(status 500) = false, want true")
	}
	if got := isCodexUsageEvent(webSocketEventInputAudioTranscriptionCompleted); got {
		t.Fatal("isCodexUsageEvent(transcription) = true, want false")
	}
	if got := classifyWebSocketUpstreamError(nil); got != webSocketSemanticClassificationUnknown {
		t.Fatalf("classifyWebSocketUpstreamError(nil) = %v, want unknown", got)
	}
	if got := classifyWebSocketUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created"}`), false); got != webSocketSemanticClassificationUnknown {
		t.Fatalf("classifyWebSocketUpstreamMessage(non-error) = %v, want unknown", got)
	}

	observer := newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil)
	usageEvent := &codexWebSocketEventEnvelope{
		Type:    webSocketEventResponseCompleted,
		EventID: "evt_1",
	}
	state := observer.captureCodexObserveState(usageEvent, []byte(`{"type":"response.completed","event_id":"evt_1"}`), true)
	if !state.needsUsage {
		t.Fatal("first usage state needsUsage = false, want true")
	}
	state = observer.captureCodexUsageState(state, &TokenUsage{PromptTokens: 5})
	if state.needsUsage {
		t.Fatal("captureCodexUsageState() kept needsUsage=true, want false")
	}

	secondState := observer.captureCodexObserveState(usageEvent, []byte(`{"type":"response.completed","event_id":"evt_1"}`), true)
	if secondState.needsUsage {
		t.Fatal("duplicate usage event requested usage parse again, want dedupe")
	}
	if snapshot := observer.Snapshot(); snapshot.TokenUsage == nil || snapshot.TokenUsage.PromptTokens != 5 {
		t.Fatalf("Snapshot().TokenUsage = %#v, want PromptTokens=5", snapshot.TokenUsage)
	}

	if got := codexUsageEventKey(&codexWebSocketEventEnvelope{Response: &codexWebSocketEventTarget{ID: "resp_1"}}); got != "response:resp_1" {
		t.Fatalf("codexUsageEventKey(response) = %q, want response:resp_1", got)
	}
	if got := codexUsageEventKey(usageEvent); got != "event:evt_1" {
		t.Fatalf("codexUsageEventKey(event) = %q, want event:evt_1", got)
	}
	if got := codexUsageEventKey(&codexWebSocketEventEnvelope{}); got != "" {
		t.Fatalf("codexUsageEventKey(empty) = %q, want empty", got)
	}

	if committed := observer.captureCommitLocked(&codexWebSocketEventEnvelope{
		Type:  webSocketEventResponseCreated,
		Error: &codexWebSocketEventError{Type: "error"},
	}, true); committed {
		t.Fatal("captureCommitLocked(error event) = true, want false")
	}
}
