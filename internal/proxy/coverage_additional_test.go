package proxy

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestActiveRequestRegistrySetStickyPerModelKeepsRequestDerivedKey(t *testing.T) {
	r := NewActiveRequestRegistry()
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.20",
		User:       "user-1",
		APIType:    "codex",
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	}

	before := r.buildKeyFromRequest(req)
	r.SetStickyPerModel(true)
	after := r.buildKeyFromRequest(req)

	if after != before {
		t.Fatalf("continuity key changed after compatibility no-op: before=%#v after=%#v", before, after)
	}
	if after.Model != "gpt-5.4" {
		t.Fatalf("key.Model = %q, want %q", after.Model, "gpt-5.4")
	}
}

func TestHandlerFailedProviderRequestReturnsSelectionError(t *testing.T) {
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Logger: zap.NewNop(),
	})
	prepareErr := errors.New("missing provider credential")

	result := handler.failedProviderRequest(context.Background(), "provider-1", prepareErr)
	if result.success {
		t.Fatal("expected provider preparation failure to leave success false")
	}
	if !errors.Is(result.err, prepareErr) {
		t.Fatalf("result.err = %v, want %v", result.err, prepareErr)
	}
}

func TestHandlerRetryUnauthorizedForwardResponseSkipsWhenAuthUnavailable(t *testing.T) {
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Logger: zap.NewNop(),
	})
	upstreamResp := &UpstreamResponse{StatusCode: http.StatusUnauthorized}

	gotResp, result, ok := handler.retryUnauthorizedForwardResponse(
		context.Background(),
		nil,
		nil,
		upstreamResp,
	)
	if !ok {
		t.Fatal("expected 401 response without auth service to keep original response")
	}
	if gotResp != upstreamResp {
		t.Fatalf("response pointer changed: got %#v want %#v", gotResp, upstreamResp)
	}
	if result.err != nil || result.success {
		t.Fatalf("result = %#v, want zero-value forward result", result)
	}
}

func TestWebSocketProviderConfigErrorErrorAndUnwrap(t *testing.T) {
	var nilErr *webSocketProviderConfigError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil Error() = %q, want empty string", got)
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", got)
	}

	baseErr := errors.New("missing managed credential")
	cfgErr := &webSocketProviderConfigError{
		missingField: "credentials",
		err:          baseErr,
	}
	if got := cfgErr.Error(); got != baseErr.Error() {
		t.Fatalf("Error() = %q, want %q", got, baseErr.Error())
	}
	if got := cfgErr.Unwrap(); !errors.Is(got, baseErr) {
		t.Fatalf("Unwrap() = %v, want wrapped %v", got, baseErr)
	}
}
