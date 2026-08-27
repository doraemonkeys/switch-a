package selector

import (
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

type stickyBindingDecision string

const stickyBindingDecisionEvicted stickyBindingDecision = "sticky_binding_evicted"

type stickyBindingDecisionReason string

const stickyBindingDecisionReasonProviderConcurrencyExhausted stickyBindingDecisionReason = "provider_concurrency_exhausted"

// observeStickyBindingDecision keeps routing identity out of telemetry. The
// server operation UUID correlates one request, while typed values preserve the
// operational explanation without exposing the IP/user-derived StickyKey.
func (s *Selector) observeStickyBindingDecision(
	req *model.SelectRequest,
	providerID string,
	decision stickyBindingDecision,
	reason stickyBindingDecisionReason,
) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Debug("selector.sticky_binding_decision",
		zap.String("operation_id", reqOperationID(req)),
		zap.String("provider_id", providerID),
		zap.String("api_type", reqAPIType(req)),
		zap.String("decision", string(decision)),
		zap.String("reason", string(reason)),
	)
}
