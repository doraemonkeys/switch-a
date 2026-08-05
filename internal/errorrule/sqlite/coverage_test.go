package sqlite

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm"
)

func TestRuleRowCodecCoversEveryActionAndRejectsCorruption(t *testing.T) {
	now := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	providerTarget, _ := errorrule.NewProviderTarget("provider-a")
	apiType := apicontract.APITypeCodex
	retryOnly, _ := errorrule.NewRetryOnlyAction(2, model.BackoffPolicy{
		InitialDelay: model.Duration(time.Millisecond), MaxDelay: model.Duration(time.Second), Multiplier: 2, Jitter: true,
	})
	retrySwitch, _ := errorrule.NewRetryThenSwitchActionWithVisibleResponse(
		1,
		model.BackoffPolicy{},
		errorrule.VisibleResponseCommit,
	)
	cases := []struct {
		id         errorrule.RuleID
		generation string
		target     errorrule.Target
		action     errorrule.Action
	}{
		{"11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", errorrule.NewGlobalTarget(), errorrule.NewPassthroughAction()},
		{"22222222-2222-4222-8222-222222222222", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", providerTarget, retryOnly},
		{"33333333-3333-4333-8333-333333333333", "cccccccc-cccc-4ccc-8ccc-cccccccccccc", providerTarget, retrySwitch},
	}
	for index, testCase := range cases {
		generation, _ := errorrule.ParseRuleGeneration(testCase.generation)
		rule := errorrule.NewRule(errorrule.RuleSpec{
			Name: "Rule", Enabled: true, Target: testCase.target, APIType: &apiType,
			Keywords: []string{"one", "two"}, MatchMode: errorrule.MatchAll, Action: testCase.action,
		}, errorrule.RuleMetadata{
			ID: testCase.id, Generation: generation, Position: int64(index), CreatedAt: now, UpdatedAt: now,
		})
		row, err := encodeRule(rule)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeRule(row)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, rule) {
			t.Fatalf("decoded=%#v want=%#v", decoded, rule)
		}
	}

	badRows := []ruleRow{
		{ID: "bad", Generation: "bad"},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "global", ProviderID: stringPointer("provider-a")},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "provider"},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "unknown"},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "global", KeywordsJSON: "[", ActionType: "passthrough"},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "global", KeywordsJSON: `[]`, ActionType: "passthrough", MaxRetries: intPointer(1)},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "global", KeywordsJSON: `[]`, ActionType: "retry_only"},
		{ID: string(cases[0].id), Generation: cases[0].generation, TargetKind: "global", KeywordsJSON: `[]`, ActionType: "unknown"},
	}
	for index, row := range badRows {
		if _, err := decodeRule(row); err == nil {
			t.Fatalf("bad row %d accepted", index)
		}
	}
}

func stringPointer(value string) *string { return &value }

func TestRepositoryDeleteReorderAndErrorContracts(t *testing.T) {
	_, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"22222222-2222-4222-8222-222222222222", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"33333333-3333-4333-8333-333333333333", "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
	)
	ctx := context.Background()
	for expected, name := range []string{"one", "two", "three"} {
		if _, err := repository.CreateRule(ctx, errorrule.Revision(expected), passthroughSpec(name, errorrule.NewGlobalTarget())); err != nil {
			t.Fatal(err)
		}
	}
	_, rules := repository.ListRules()
	ordered := []errorrule.RuleID{rules[2].ID, rules[0].ID, rules[1].ID}
	reordered, err := repository.ReorderRules(ctx, 3, ordered)
	if err != nil || reordered.Revision != 4 {
		t.Fatalf("reordered=%#v err=%v", reordered, err)
	}
	noOp, err := repository.ReorderRules(ctx, 4, ordered)
	if err != nil || noOp.Changed || repository.CurrentRuleSet().Revision() != 4 {
		t.Fatalf("no-op reorder=%#v err=%v", noOp, err)
	}
	if _, err := repository.ReorderRules(ctx, 4, ordered[:2]); err == nil {
		t.Fatal("short reorder accepted")
	}
	duplicate := []errorrule.RuleID{ordered[0], ordered[0], ordered[2]}
	if _, err := repository.ReorderRules(ctx, 4, duplicate); err == nil {
		t.Fatal("duplicate reorder accepted")
	}
	deleted, err := repository.DeleteRule(ctx, 4, ordered[0])
	if err != nil || deleted.Revision != 5 || len(deleted.Rules) != 2 {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	if _, err := repository.DeleteRule(ctx, 5, ordered[0]); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("second delete error=%v", err)
	}
	if _, _, err := repository.GetRule(ordered[0]); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("GetRule(deleted) error=%v", err)
	}
	missingSpec := passthroughSpec("missing", errorrule.NewGlobalTarget())
	if _, err := repository.UpdateRule(ctx, 5, ordered[0], missingSpec); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("UpdateRule(missing) error=%v", err)
	}
	if _, err := repository.Coordinate(ctx, nil, nil); err == nil {
		t.Fatal("nil mutation accepted")
	}
	if err := repository.PublishRuleSet(nil); err == nil {
		t.Fatal("nil publication accepted")
	}
	mismatch := &RevisionMismatchError{Expected: 1, Current: 2}
	if !strings.Contains(mismatch.Error(), "expected 1, current 2") || !errors.Is(mismatch, ErrRevisionMismatch) {
		t.Fatalf("mismatch=%v", mismatch)
	}
}

func TestRepositoryZeroValueReadAndRemovalHelpers(t *testing.T) {
	zero := &Repository{}
	if revision, rules := zero.ListRules(); revision != 0 || rules != nil {
		t.Fatalf("zero ListRules revision=%d rules=%#v", revision, rules)
	}
	if _, _, err := zero.GetRule("11111111-1111-4111-8111-111111111111"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("zero GetRule error=%v", err)
	}
	if (realClock{}).Now().IsZero() {
		t.Fatal("real clock returned zero")
	}
	global := errorrule.NewRule(passthroughSpec("global", errorrule.NewGlobalTarget()), errorrule.RuleMetadata{})
	otherTarget, _ := errorrule.NewProviderTarget("provider-b")
	other := errorrule.NewRule(passthroughSpec("other", otherTarget), errorrule.RuleMetadata{})
	retained := RemoveProviderRules([]errorrule.Rule{global, other}, "provider-a")
	if len(retained) != 2 {
		t.Fatalf("retained=%#v", retained)
	}
}

func TestRepositoryRevisionOverflowAndConstructors(t *testing.T) {
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("Open without DB accepted")
	}
	if err := Migrate(context.Background(), nil); err == nil {
		t.Fatal("Migrate without DB accepted")
	}
	db, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	created, err := repository.CreateRule(context.Background(), 0, passthroughSpec("one", errorrule.NewGlobalTarget()))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE internal_error_rule_set_meta SET revision = ?", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	_, err = repository.Coordinate(context.Background(), nil, func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
		current[0].Name = "changed"
		return current, nil
	})
	if !errors.Is(err, ErrRevisionOverflow) {
		t.Fatalf("overflow error=%v created=%#v", err, created)
	}
}

func TestImportValidationBranches(t *testing.T) {
	current := []errorrule.Rule{}
	preserved, counts, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModePreserve})
	if err != nil || preserved != nil || counts != (ImportCounts{}) {
		t.Fatalf("preserve=%#v counts=%#v err=%v", preserved, counts, err)
	}
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: "bad"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeSelection}); err == nil {
		t.Fatal("empty selection accepted")
	}
	valid := ImportedRule{ID: "11111111-1111-4111-8111-111111111111", RuleSpec: passthroughSpec("one", errorrule.NewGlobalTarget())}
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeFull, Rules: []ImportedRule{valid, valid}}); err == nil {
		t.Fatal("duplicate import accepted")
	}
	invalidID := valid
	invalidID.ID = "bad"
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeFull, Rules: []ImportedRule{invalidID}}); err == nil {
		t.Fatal("invalid ID accepted")
	}
	invalidSpec := valid
	invalidSpec.Name = ""
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeFull, Rules: []ImportedRule{invalidSpec}}); err == nil {
		t.Fatal("invalid spec accepted")
	}
	tooMany := make([]ImportedRule, errorrule.MaxRuleCount+1)
	if _, _, err := BuildImportCandidate(current, ImportRequest{Mode: ImportModeFull, Rules: tooMany}); !errors.Is(err, ErrRuleCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
}

func TestStatsPersistenceMissingRollbackAndMaximumTimestamp(t *testing.T) {
	_, repository, _ := openTestRepository(t,
		"11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	created, _ := repository.CreateRule(context.Background(), 0, passthroughSpec("one", errorrule.NewGlobalTarget()))
	handle := statistics.HandleFor(created.Rules[0])
	late := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	early := late.Add(-time.Hour)
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{Handle: handle, HitCount: 2, LastHitAt: late}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{Handle: handle, HitCount: 3, LastHitAt: early}}); err != nil {
		t.Fatal(err)
	}
	stats, err := repository.ListStats(context.Background())
	if err != nil || stats[0].HitCount != 5 || stats[0].LastHitAt == nil || !stats[0].LastHitAt.Equal(late) {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	invalid := statistics.Handle{RuleID: handle.RuleID}
	if _, err := repository.ApplyRuleStatDeltas(context.Background(), []statistics.Delta{
		{Handle: handle, HitCount: 10, LastHitAt: late},
		{Handle: invalid, HitCount: 1, LastHitAt: late},
	}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	stats, _ = repository.ListStats(context.Background())
	if stats[0].HitCount != 5 {
		t.Fatalf("failed batch partially committed: %#v", stats[0])
	}
	if result, err := repository.ApplyRuleStatDeltas(context.Background(), nil); err != nil || len(result.Missing) != 0 {
		t.Fatalf("empty apply result=%#v err=%v", result, err)
	}
}
