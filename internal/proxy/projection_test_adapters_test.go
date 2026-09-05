package proxy

import (
	"bytes"
	"context"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress/semantic"
	"net/http"
)

// The original fixture suites now exercise the sole production projector.
func ExtractModel(r *http.Request, apiType string, body []byte) string {
	if apiType == APITypeGemini {
		return requestHeadModel(r, apiType)
	}
	return extractModelFromJSON(body)
}
func extractModelFromJSON(body []byte) string {
	return semantic.Project(context.Background(), bytes.NewReader(body), semantic.Options{}).Model.Value
}
func ExtractRequestedReasoning(apiType, path string, body []byte) model.RequestedReasoningObservation {
	return semantic.Project(context.Background(), bytes.NewReader(body), semantic.Options{ReasoningContract: requestReasoningContract(apiType, path)}).Reasoning.Value
}
