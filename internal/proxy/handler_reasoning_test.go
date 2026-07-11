package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestHandler_ServeHTTP_PersistsRequestedReasoningObservation(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantState  model.ReasoningObservationState
		wantEffort *string
	}{
		{
			name:       "captured",
			path:       RouteClaudeMessages,
			body:       `{"model":"test","output_config":{"effort":"high"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:      "absent",
			path:      RouteClaudeMessages,
			body:      `{"model":"test"}`,
			wantState: model.ReasoningObservationAbsent,
		},
		{
			name:       "grok scalar effort captured",
			path:       RouteGrokChatCompletionsV1,
			body:       `{"model":"grok-4","reasoning_effort":"high"}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)
			waitFor(t, func() bool { return store.LogsLen() == 1 }, testPollTimeout)

			log := store.LastLog()
			if log == nil {
				t.Fatal("expected persisted request log")
			}
			assertReasoningObservation(
				t,
				log.RequestedReasoningObservation,
				test.wantState,
				test.wantEffort,
				nil,
				nil,
			)
		})
	}
}
