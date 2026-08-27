package store

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func (s *SQLiteStore) ListRoutingPoliciesByAPIType(ctx context.Context, apiType string) ([]model.RoutingPolicy, error) {
	var policies []model.RoutingPolicy
	if err := routingPolicyQuery(s.db.WithContext(ctx)).
		Where("api_type = ? AND enabled = ?", apiType, true).
		Order("id ASC").
		Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("list routing policies by api type %q: %w", apiType, err)
	}
	return policies, nil
}
