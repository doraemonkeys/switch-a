package proxy

import (
	"testing"

	"github.com/coder/websocket"
)

func TestCodexWebSocketMessageObserver_SnapshotAggregatesModelAndUsage(t *testing.T) {
	t.Parallel()

	observer := newWebSocketMessageObserver(APITypeCodex, ModelUnknown, nil, nil)
	if observer == nil {
		t.Fatal("expected non-nil observer for codex websocket")
	}

	observer.ObserveClientMessage(websocket.MessageText, []byte(`{"type":"response.create","response":{"model":"gpt-realtime-preview"}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created","event_id":"evt_session","session":{"model":"gpt-realtime-2025-08-28"}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.completed","event_id":"evt_resp_1","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_token_details":{"cached_tokens":3}}}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.completed","event_id":"evt_resp_1_dup","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`))
	// Transcription events are billed under a separate ASR model and must NOT
	// be merged into the realtime model usage total.
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","event_id":"evt_transcript_1","usage":{"input_tokens":4,"output_tokens":0,"total_tokens":4}}`))

	snapshot := observer.Snapshot()
	if snapshot.Model != "gpt-realtime-2025-08-28" {
		t.Fatalf("snapshot.Model = %q, want %q", snapshot.Model, "gpt-realtime-2025-08-28")
	}
	if snapshot.TokenUsage == nil {
		t.Fatal("expected non-nil token usage snapshot")
	}
	if snapshot.TokenUsage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", snapshot.TokenUsage.PromptTokens)
	}
	if snapshot.TokenUsage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", snapshot.TokenUsage.CompletionTokens)
	}
	if snapshot.TokenUsage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", snapshot.TokenUsage.TotalTokens)
	}
	if snapshot.TokenUsage.CacheReadInputTokens != 3 {
		t.Errorf("CacheReadInputTokens = %d, want 3", snapshot.TokenUsage.CacheReadInputTokens)
	}
}

func TestCodexWebSocketMessageObserver_PrefersServerModelWhenClientDidNotProvideOne(t *testing.T) {
	t.Parallel()

	observer := newWebSocketMessageObserver(APITypeCodex, ModelUnknown, nil, nil)
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created","event_id":"evt_session","session":{"model":"gpt-realtime-2025-08-28"}}`))

	snapshot := observer.Snapshot()
	if snapshot.Model != "gpt-realtime-2025-08-28" {
		t.Fatalf("snapshot.Model = %q, want %q", snapshot.Model, "gpt-realtime-2025-08-28")
	}
}

func TestCodexWebSocketMessageObserver_UsesLatestUpstreamModelAtSamePriority(t *testing.T) {
	t.Parallel()

	observer := newWebSocketMessageObserver(APITypeCodex, ModelUnknown, nil, nil)
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"session.created","event_id":"evt_session","session":{"model":"gpt-realtime-2025-08-28"}}`))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte(`{"type":"response.created","event_id":"evt_response","response":{"id":"resp_live","model":"gpt-5.4"}}`))

	snapshot := observer.Snapshot()
	if snapshot.Model != "gpt-5.4" {
		t.Fatalf("snapshot.Model = %q, want %q", snapshot.Model, "gpt-5.4")
	}
}

func TestQuickExtractEventType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "session.created", data: `{"type":"session.created","session":{"model":"m"}}`, want: "session.created"},
		{name: "response.done", data: `{"type":"response.done","response":{"id":"r1"}}`, want: "response.done"},
		{name: "audio append", data: `{"type":"input_audio_buffer.append","audio":"` + string(make([]byte, 1024)) + `"}`, want: "input_audio_buffer.append"},
		{name: "no type field", data: `{"event_id":"e1","data":{}}`, want: ""},
		{name: "empty", data: ``, want: ""},
		{name: "malformed json", data: `not json at all`, want: ""},
		{name: "type not a string", data: `{"type":123}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := quickExtractEventType([]byte(tt.data))
			if got != tt.want {
				t.Errorf("quickExtractEventType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsObservableEventType(t *testing.T) {
	t.Parallel()
	observable := []string{
		"session.created", "session.updated", "session.update",
		"response.done", "response.completed", "response.created", "response.create",
	}
	nonObservable := []string{
		"input_audio_buffer.append", "input_audio_buffer.commit",
		"response.audio.delta", "response.text.delta",
		"conversation.item.input_audio_transcription.completed",
		"conversation.item.created",
	}
	for _, et := range observable {
		if !isObservableEventType(et) {
			t.Errorf("isObservableEventType(%q) = false, want true", et)
		}
	}
	for _, et := range nonObservable {
		if isObservableEventType(et) {
			t.Errorf("isObservableEventType(%q) = true, want false", et)
		}
	}
}

func TestCodexWebSocketMessageObserver_SkipsAudioBufferAppend(t *testing.T) {
	t.Parallel()

	var parseCount int
	observer := newWebSocketMessageObserver(APITypeCodex, "gpt-realtime", nil, func(_ WebSocketObservation) {
		parseCount++
	})

	// Audio append events must be skipped without triggering a full JSON parse.
	for i := 0; i < 100; i++ {
		observer.ObserveClientMessage(websocket.MessageText, []byte(`{"type":"input_audio_buffer.append","audio":"AAAA"}`))
	}
	// Binary messages are already skipped by the messageType check.
	observer.ObserveClientMessage(websocket.MessageBinary, make([]byte, 1024))

	snapshot := observer.Snapshot()
	if snapshot.TokenUsage != nil {
		t.Error("expected nil TokenUsage after only audio events")
	}
	if parseCount != 0 {
		t.Errorf("onUpdate called %d times, want 0 for audio-only traffic", parseCount)
	}
}
