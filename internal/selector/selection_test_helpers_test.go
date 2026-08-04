package selector

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func (s *Selector) selectForTest(
	t *testing.T,
	ctx context.Context,
	req *model.SelectRequest,
) (*model.Provider, error) {
	t.Helper()
	result, err := s.SelectWithMetadata(ctx, req)
	return ownSelectionForTest(t, result, err)
}

func (s *Selector) selectExcludingForTest(
	t *testing.T,
	ctx context.Context,
	req *model.SelectRequest,
	excluded map[string]bool,
) (*model.Provider, error) {
	t.Helper()
	result, err := s.SelectExcludingWithMetadata(ctx, req, excluded)
	return ownSelectionForTest(t, result, err)
}

func ownSelectionForTest(t *testing.T, result *SelectResult, err error) (*model.Provider, error) {
	t.Helper()
	if result == nil {
		return nil, err
	}
	if result.Lease != nil {
		t.Cleanup(func() { result.Lease.Release() })
	}
	return result.Provider(), err
}
