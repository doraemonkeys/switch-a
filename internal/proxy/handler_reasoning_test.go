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
		body       string
		wantState  model.ReasoningObservationState
		wantEffort *string
	}{
		{
			name:       "captured",
			body:       `{"model":"test","output_config":{"effort":"high"}}`,
			wantState:  model.ReasoningObservationCaptured,
			wantEffort: reasoningStringPointer("high"),
		},
		{
			name:      "absent",
			body:      `{"model":"test"}`,
			wantState: model.ReasoningObservationAbsent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
			req := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, strings.NewReader(test.body))
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
