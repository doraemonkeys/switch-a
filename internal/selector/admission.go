package selector

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// PrepareAdmission pins routing input before deciding whether a body model is
// required. Every later selection and reservation uses this same catalog.
func PrepareAdmission(ctx context.Context, source any, request *model.SelectRequest) (bool, error) {
	policies, err := listRoutingPoliciesByAPIType(ctx, source, reqAPIType(request))
	if err != nil {
		return false, err
	}
	request.RoutingCatalog = model.NewRoutingCatalog(policies)
	return selectionConsumesHiddenModel(policies, request), nil
}

func selectionRoutingPolicies(ctx context.Context, source any, request *model.SelectRequest) ([]model.RoutingPolicy, error) {
	if request != nil && request.RoutingCatalog != nil {
		return request.RoutingCatalog.Policies(), nil
	}
	return listRoutingPoliciesByAPIType(ctx, source, reqAPIType(request))
}
