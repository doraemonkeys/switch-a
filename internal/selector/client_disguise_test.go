package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type selectionDisguise struct {
	excluded map[string]bool
	commits  int
	failure  error
}

func (d *selectionDisguise) Evaluate(_ context.Context, p *model.Provider) (clientdisguise.Candidate, error) {
	return clientdisguise.Candidate{Decision: clientdisguise.PlatformDecision{Allowed: !d.excluded[p.ID], Reason: "platform_mismatch"}}, d.failure
}
func (d *selectionDisguise) Commit(context.Context, *model.Provider) (clientdisguise.TargetSnapshot, error) {
	d.commits++
	return clientdisguise.TargetSnapshot{}, nil
}
func (d *selectionDisguise) Exclusions() []model.DisguiseExclusion {
	result := []model.DisguiseExclusion{}
	for id, excluded := range d.excluded {
		if excluded {
			result = append(result, model.DisguiseExclusion{ProviderID: id, Reason: "platform_mismatch"})
		}
	}
	return result
}
func TestPlatformGateCoversPreferredStickyStrategyAndReservation(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	for _, id := range []string{"excluded", "allowed"} {
		store.providers = append(store.providers, model.Provider{ID: id, Name: id, Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: id, APIType: "codex"}}})
	}
	disguise := &selectionDisguise{excluded: map[string]bool{"excluded": true}}
	req := &model.SelectRequest{APIType: "codex", OperationID: "op", StickyMode: model.StickyModeAPIType, PreferredRouteTargetID: "excluded", ClientDisguise: disguise}
	sticky := NewMemoryStickyCache(&mockClock{now: time.Now()})
	selector := NewSelector(Config{Store: store, Clock: &mockClock{now: time.Now()}, StickyCache: sticky})
	result, err := selector.SelectWithMetadata(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider().ID != "allowed" {
		t.Fatal(result.Provider().ID)
	}
	result.Lease.Release()
	if disguise.commits != 0 {
		t.Fatal("selection bound profile before physical dispatch")
	}
	reservation, err := selector.ReserveAlternate(ctx, AlternateReservationRequest{Request: req})
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	if disguise.commits != 0 {
		t.Fatal("reservation bound profile")
	}
	disguise.excluded["allowed"] = true
	_, err = selector.SelectWithMetadata(ctx, req)
	var platform *PlatformSelectionError
	if !errors.As(err, &platform) || !errors.Is(err, internal.ErrNoProvider) || len(platform.Exclusions) != 2 {
		t.Fatal(err)
	}
	if platform.Error() == "" {
		t.Fatal("empty diagnostic")
	}
	disguise.failure = errors.New("profile repository unavailable")
	if _, err := selector.SelectWithMetadata(ctx, req); !errors.Is(err, disguise.failure) {
		t.Fatal(err)
	}
}
