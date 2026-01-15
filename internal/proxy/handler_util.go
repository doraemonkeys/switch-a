package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"switch-a/internal"
	"switch-a/internal/defaults"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// logInsertTimeout is the maximum time allowed for inserting a request log.
// This prevents goroutine accumulation if the database is slow or blocked.
const logInsertTimeout = 2 * time.Second

// handleNoProvider handles the case when no provider is available.
func (h *Handler) handleNoProvider(pctx *proxyContext) {
	h.logger.Warn("no providers available", zap.String("api_type", pctx.apiType))
	h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	go h.logRequest(pctx, nil, StatusCodeNoResponse, false, false, internal.ErrNoProvider, time.Since(pctx.startTime))
}

// handleExhaustedRetries handles exhausted retry attempts.
func (h *Handler) handleExhaustedRetries(pctx *proxyContext, lastErr error) {
	if lastErr != nil {
		h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderExhausted, "All providers failed")
	} else {
		// No lastErr means all providers were excluded/unavailable without actually failing.
		// Log this edge case for visibility, as it indicates a selection issue rather than provider failure.
		h.logger.Warn("exhausted retries with no provider errors", zap.String("api_type", pctx.apiType))
		h.writeGatewayError(pctx.w, http.StatusServiceUnavailable, ErrCodeProviderUnavailable, fmt.Sprintf("No available provider for api_type: %s", pctx.apiType))
	}
}

// markSuccess marks a successful request for the provider.
func (h *Handler) markSuccess(ctx context.Context, providerID string) {
	if h.health != nil {
		h.health.MarkSuccess(ctx, providerID)
	}
}

// markFailure marks a failed request for the provider.
func (h *Handler) markFailure(ctx context.Context, providerID string, err error) {
	if h.health != nil {
		h.health.MarkFailure(ctx, providerID, err)
	}
}

// releaseConcurrency releases the concurrency slot for a provider.
func (h *Handler) releaseConcurrency(providerID string) {
	if h.selector != nil {
		h.selector.ReleaseConcurrency(providerID)
	}
}

// writeGatewayError writes a gateway error response.
func (h *Handler) writeGatewayError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	err := model.NewGatewayError(code, message)
	if encErr := json.NewEncoder(w).Encode(err); encErr != nil { // coverage-ignore -- JSON encoding of simple struct rarely fails
		h.logger.Error("failed to encode error response", zap.Error(encErr))
	}
}

// logRequest logs the request asynchronously.
// Note: Uses context.Background() with timeout because this runs after the HTTP response
// completes and the request context may already be cancelled.
func (h *Handler) logRequest(pctx *proxyContext, provider *model.Provider, statusCode int, success bool, isSSE bool, err error, latency time.Duration) {
	log := &model.RequestLog{
		RequestID:  pctx.requestID,
		APIType:    pctx.info.APIType,
		Model:      pctx.info.Model,
		ClientIP:   pctx.info.ClientIP,
		UserID:     pctx.info.UserID,
		StatusCode: statusCode,
		LatencyMs:  latency.Milliseconds(),
		Success:    success,
		IsSSE:      isSSE,
		RetryCount: max(0, len(pctx.attempts)-1),
		IsSticky:   pctx.isSticky,
		CreatedAt:  time.Now(),
	}

	if provider != nil {
		log.ProviderID = provider.ID
	}

	if err != nil { // coverage-ignore -- error logging path
		log.ErrorMsg = err.Error()
	}

	// Use timeout to prevent goroutine accumulation if database is slow or blocked
	ctx, cancel := context.WithTimeout(context.Background(), logInsertTimeout)
	defer cancel()

	if insertErr := h.store.InsertLog(ctx, log); insertErr != nil { // coverage-ignore -- log insert errors are logged but don't affect response
		h.logger.Error("failed to insert request log", zap.Error(insertErr))
	}

	// Store attempts if there are any (for retry tracking)
	if len(pctx.attempts) > 0 {
		if insertErr := h.store.InsertAttempts(ctx, pctx.attempts); insertErr != nil { // coverage-ignore -- attempt insert errors are logged but don't affect response
			h.logger.Error("failed to insert request attempts", zap.Error(insertErr))
		}
	}
}

// shouldFailover determines if we should try another provider instead of
// forwarding this response to the client. Returns true for provider-side issues
// that may be resolved by switching to a different provider:
//   - Server errors (5xx): temporary upstream issues
//   - Payment/quota errors (402): provider billing limits or quota exhausted
//   - Rate limiting (429): provider overloaded
//   - Auth errors (401, 403): provider misconfiguration (wrong/expired API key)
func shouldFailover(statusCode int) bool {
	return statusCode >= defaults.StatusServerError ||
		statusCode == defaults.StatusPaymentRequired ||
		statusCode == defaults.StatusTooManyRequests ||
		statusCode == defaults.StatusUnauthorized ||
		statusCode == defaults.StatusForbidden
}

// shouldForceProviderSwitch determines if the status code indicates a permanent
// failure that won't be resolved by retrying the same provider. For these codes,
// we should immediately switch to a different provider instead of wasting retries:
//   - 402 Payment Required: billing/quota issue, won't change with retries
//   - 401 Unauthorized: authentication failure, won't change with retries
//   - 403 Forbidden: access denied, won't change with retries
//
// Note: 429 (Too Many Requests) and 5xx errors are NOT included because they may
// be transient and could succeed on retry with the same provider.
func shouldForceProviderSwitch(statusCode int) bool {
	return statusCode == defaults.StatusPaymentRequired ||
		statusCode == defaults.StatusUnauthorized ||
		statusCode == defaults.StatusForbidden
}

// buildFullURL constructs the full upstream URL.
// It properly joins the baseURL's existing path (if any) with the given path.
// For example: baseURL="https://api.openai.com/v1", path="/chat/completions"
// yields "https://api.openai.com/v1/chat/completions".
func (h *Handler) buildFullURL(baseURL, path, query string) string {
	// url.JoinPath handles path joining correctly:
	// - Preserves the base URL's scheme, host, and existing path
	// - Properly joins paths (handles slashes, dots, etc.)
	joined, err := url.JoinPath(baseURL, path)
	if err != nil {
		// Invalid base URL - fall back to string concatenation with proper slash handling.
		// Warning: if the base URL is fundamentally malformed, this fallback may produce
		// an invalid URL that will fail during the HTTP request with a less clear error.
		h.logger.Warn("invalid base URL, falling back to string concatenation (may produce invalid URL)",
			zap.String("base_url", baseURL),
			zap.Error(err),
		)
		if !strings.HasSuffix(baseURL, "/") && !strings.HasPrefix(path, "/") {
			joined = baseURL + "/" + path
		} else {
			joined = baseURL + path
		}
	}

	if query != "" {
		return joined + "?" + query
	}
	return joined
}
