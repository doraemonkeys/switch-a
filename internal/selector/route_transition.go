package selector

import "github.com/doraemonkeys/switch-a/internal/model"

type providerEligibilityMode struct {
	checkHealth          bool
	checkRouteTransition bool
	checkRouting         bool
	checkAuthority       bool
}

func selectionEligibilityMode() providerEligibilityMode {
	return providerEligibilityMode{
		checkHealth:          true,
		checkRouteTransition: true,
		checkRouting:         true,
		checkAuthority:       true,
	}
}

func existingRouteEligibilityMode(checkHealth bool) providerEligibilityMode {
	return providerEligibilityMode{
		checkHealth:    checkHealth,
		checkRouting:   true,
		checkAuthority: true,
	}
}

// routeTransitionAllowsProvider applies only constraints that govern moving to
// a candidate. Existing-route retry and active reuse still pass through the
// shared authority/session/routing closure, but are not new switch transitions.
func routeTransitionAllowsProvider(provider *model.Provider, req *model.SelectRequest, maxProviderSwitches int) bool {
	if !model.IsProviderSwitchAllowed(provider, reqProviderSwitchHistory(req), maxProviderSwitches) {
		return false
	}
	return reqSwitchMode(req) != model.SwitchModeFailover ||
		model.IsFailoverVendorAllowed(provider, reqProviderContinuityContext(req))
}

func reqSwitchMode(req *model.SelectRequest) model.SwitchMode {
	if req == nil {
		return model.SwitchModeInitial
	}
	return req.EffectiveSwitchMode()
}

func reqProviderSwitchHistory(req *model.SelectRequest) *model.ProviderSwitchHistory {
	if req == nil {
		return nil
	}
	return req.EffectiveProviderSwitchHistory()
}

func reqProviderContinuityContext(req *model.SelectRequest) *model.ProviderContinuityContext {
	if req == nil {
		return nil
	}
	return req.EffectiveProviderContinuityContext()
}

func (e *ProviderSelectionEligibility) reqMaxProviderSwitches() int {
	if e == nil || e.req == nil {
		return 0
	}
	return e.req.EffectiveMaxProviderSwitches()
}
