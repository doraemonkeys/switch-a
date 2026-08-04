package errorruleapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/store"
)

type RuleService interface {
	CurrentRuleSet() *errorrule.CompiledRuleSet
	ListRules() (errorrule.Revision, []errorrule.Rule)
	GetRule(errorrule.RuleID) (errorrule.Revision, errorrule.Rule, error)
	CreateRule(context.Context, errorrule.Revision, errorrule.RuleSpec) (errorrulesqlite.MutationResult, error)
	UpdateRule(context.Context, errorrule.Revision, errorrule.RuleID, errorrule.RuleSpec) (errorrulesqlite.MutationResult, error)
	DeleteRule(context.Context, errorrule.Revision, errorrule.RuleID) (errorrulesqlite.MutationResult, error)
	ReorderRules(context.Context, errorrule.Revision, []errorrule.RuleID) (errorrulesqlite.MutationResult, error)
}

type RuleStatsReader interface {
	ListStatsSnapshot(context.Context) (errorrule.Revision, []errorrule.RuleStats, error)
}

type RuleStatsOverlay interface {
	Overlay([]errorrule.RuleStats, []errorrule.Rule) []errorrule.RuleStats
}

type ProviderCatalog interface {
	GetProvider(context.Context, string) (*model.Provider, error)
}

type MessageAnalyzer interface {
	Analyze(context.Context, MessageAnalysisInput, func(AnalyzedError) bool) MessageAnalysisResult
}

type MessageAnalysisInput struct {
	APIType         apicontract.APIType
	ContentType     string
	ContentEncoding string
	Body            []byte
}

type AnalyzedError struct {
	FrameIndex int
	Fields     responseanalysis.SemanticFields
}

type MessageAnalysisResult struct {
	ProtocolID *apicontract.ResponseProtocolID
	Failure    responseanalysis.AnalysisFailureReason
}

type TestMessageInput struct {
	APIType         apicontract.APIType
	ProviderID      *string
	ContentType     string
	ContentEncoding string
	Body            []byte
}

type service struct {
	rules     RuleService
	stats     RuleStatsReader
	overlay   RuleStatsOverlay
	providers ProviderCatalog
	analyzer  MessageAnalyzer
}

const maxStatsSnapshotReadAttempts = 3

var errStatsSnapshotDidNotConverge = errors.New("internal-error stats snapshot did not converge")

type providerNotFoundError struct {
	providerID string
}

func (e *providerNotFoundError) Error() string {
	return "provider not found: " + e.providerID
}

func (s *service) listRules() (errorrule.Revision, []errorrule.Rule) {
	return s.rules.ListRules()
}

func (s *service) getRule(id errorrule.RuleID) (errorrule.Revision, errorrule.Rule, error) {
	return s.rules.GetRule(id)
}

func (s *service) createRule(
	ctx context.Context,
	expected errorrule.Revision,
	spec errorrule.RuleSpec,
) (errorrulesqlite.MutationResult, errorrule.Rule, error) {
	before, err := s.snapshotAtRevision(expected)
	if err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	if err := s.ensureTargetProvider(ctx, spec); err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	result, err := s.rules.CreateRule(ctx, expected, spec)
	if err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	existing := make(map[errorrule.RuleID]struct{}, len(before.Rules()))
	for _, rule := range before.Rules() {
		existing[rule.ID] = struct{}{}
	}
	for _, rule := range result.Rules {
		if _, found := existing[rule.ID]; !found {
			return result, rule, nil
		}
	}
	return errorrulesqlite.MutationResult{}, errorrule.Rule{}, fmt.Errorf("created rule is absent from mutation result")
}

func (s *service) updateRule(
	ctx context.Context,
	expected errorrule.Revision,
	id errorrule.RuleID,
	spec errorrule.RuleSpec,
) (errorrulesqlite.MutationResult, errorrule.Rule, error) {
	if _, err := s.snapshotAtRevision(expected); err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	if err := s.ensureTargetProvider(ctx, spec); err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	result, err := s.rules.UpdateRule(ctx, expected, id, spec)
	if err != nil {
		return errorrulesqlite.MutationResult{}, errorrule.Rule{}, err
	}
	for _, rule := range result.Rules {
		if rule.ID == id {
			return result, rule, nil
		}
	}
	return errorrulesqlite.MutationResult{}, errorrule.Rule{}, fmt.Errorf("updated rule is absent from mutation result")
}

func (s *service) deleteRule(
	ctx context.Context,
	expected errorrule.Revision,
	id errorrule.RuleID,
) (errorrulesqlite.MutationResult, error) {
	return s.rules.DeleteRule(ctx, expected, id)
}

func (s *service) reorderRules(
	ctx context.Context,
	expected errorrule.Revision,
	ordered []errorrule.RuleID,
) (errorrulesqlite.MutationResult, error) {
	snapshot, err := s.snapshotAtRevision(expected)
	if err != nil {
		return errorrulesqlite.MutationResult{}, err
	}
	current := snapshot.Rules()
	if len(ordered) != len(current) {
		return errorrulesqlite.MutationResult{}, validationError(
			"ordered_rule_ids", "reorder must contain every current rule exactly once", nil,
		)
	}
	currentIDs := make(map[errorrule.RuleID]struct{}, len(current))
	for _, rule := range current {
		currentIDs[rule.ID] = struct{}{}
	}
	for index, id := range ordered {
		if _, exists := currentIDs[id]; !exists {
			return errorrulesqlite.MutationResult{}, validationError(
				fmt.Sprintf("ordered_rule_ids[%d]", index), "reorder contains an unknown rule", nil,
			)
		}
		delete(currentIDs, id)
	}
	return s.rules.ReorderRules(ctx, expected, ordered)
}

func (s *service) listStats(ctx context.Context) (errorrule.Revision, []errorrule.RuleStats, error) {
	for attempt := 0; attempt < maxStatsSnapshotReadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		snapshot, err := errorrule.PinRuleSet(s.rules)
		if err != nil {
			return 0, nil, err
		}
		persistedRevision, persisted, err := s.stats.ListStatsSnapshot(ctx)
		if err != nil {
			return 0, nil, err
		}
		current := s.rules.CurrentRuleSet()
		if persistedRevision != snapshot.Revision() || current != snapshot ||
			current == nil || current.Revision() != persistedRevision {
			continue
		}
		return snapshot.Revision(), s.overlay.Overlay(persisted, snapshot.Rules()), nil
	}
	return 0, nil, fmt.Errorf("%w after %d attempts", errStatsSnapshotDidNotConverge, maxStatsSnapshotReadAttempts)
}

func (s *service) testMessage(
	ctx context.Context,
	expected *errorrule.Revision,
	input TestMessageInput,
) (TestMessageResponse, error) {
	snapshot, err := errorrule.PinRuleSet(s.rules)
	if err != nil {
		return TestMessageResponse{}, err
	}
	if expected != nil && *expected != snapshot.Revision() {
		return TestMessageResponse{}, &errorrulesqlite.RevisionMismatchError{
			Expected: *expected, Current: snapshot.Revision(),
		}
	}
	if input.ProviderID != nil {
		if err := s.ensureProvider(ctx, *input.ProviderID); err != nil {
			return TestMessageResponse{}, err
		}
	}

	response := TestMessageResponse{
		SchemaVersion: SchemaVersion, RuleSetRevision: snapshot.Revision().String(),
		AnalysisStatus: "complete", Errors: make([]TestMessageError, 0),
	}
	scope := errorrule.RequestScope{APIType: input.APIType}
	if input.ProviderID != nil {
		scope.ProviderID = errorrule.ProviderID(*input.ProviderID)
	}
	analysis := s.analyzer.Analyze(ctx, MessageAnalysisInput{
		APIType: input.APIType, ContentType: input.ContentType,
		ContentEncoding: input.ContentEncoding, Body: input.Body,
	}, func(observed AnalyzedError) bool {
		fields := errorrule.SemanticFields{
			Type: observed.Fields.Type, Code: observed.Fields.Code,
			Message: observed.Fields.Message, Reason: observed.Fields.Reason,
		}
		matches := snapshot.Match(scope, fields)
		errorIndex := len(response.Errors)
		wireError := TestMessageError{
			FrameIndex: observed.FrameIndex,
			Type:       optionalSemanticString(observed.Fields.Type, observed.Fields.HasType()),
			Code:       optionalSemanticString(observed.Fields.Code, observed.Fields.HasCode()),
			Message:    optionalSemanticString(observed.Fields.Message, observed.Fields.HasMessage()),
			Reason:     optionalSemanticString(observed.Fields.Reason, observed.Fields.HasReason()),
			Matches:    make([]TestMessageMatch, len(matches.All)),
		}
		for index, match := range matches.All {
			wireError.Matches[index] = newMessageMatch(match)
		}
		response.Errors = append(response.Errors, wireError)
		if matches.Winner == nil {
			return true
		}
		response.DecisiveErrorIndex = &errorIndex
		response.Winner = &TestMessageWinner{
			ErrorIndex: errorIndex, TestMessageMatch: newMessageMatch(*matches.Winner),
		}
		// Runtime decisions latch the first matching semantic error. Stopping the
		// same stream here prevents later malformed output from changing Test
		// Message's result after runtime would already have discarded the response.
		return false
	})
	response.ResponseProtocolID = cloneProtocolID(analysis.ProtocolID)
	if analysis.Failure != "" {
		response.AnalysisStatus = "fail_open"
		reason := analysis.Failure
		response.AnalysisReason = &reason
	}
	return response, nil
}

func optionalSemanticString(value string, present bool) *string {
	if !present {
		return nil
	}
	clone := strings.Clone(value)
	return &clone
}

func newMessageMatch(match errorrule.RuleMatch) TestMessageMatch {
	return TestMessageMatch{
		RuleID:                match.Rule.ID,
		MatchedKeywords:       append([]string(nil), match.MatchedKeywords...),
		MatchedKeywordIndexes: append([]int(nil), match.MatchedKeywordIndexes...),
		MatchedFields:         append([]errorrule.SemanticField(nil), match.MatchedFields...),
	}
}

func (s *service) snapshotAtRevision(expected errorrule.Revision) (*errorrule.CompiledRuleSet, error) {
	snapshot, err := errorrule.PinRuleSet(s.rules)
	if err != nil {
		return nil, err
	}
	if snapshot.Revision() != expected {
		return nil, &errorrulesqlite.RevisionMismatchError{Expected: expected, Current: snapshot.Revision()}
	}
	return snapshot, nil
}

func (s *service) ensureTargetProvider(ctx context.Context, spec errorrule.RuleSpec) error {
	providerID, scoped := spec.Target.ProviderID()
	if !scoped {
		return nil
	}
	return s.ensureProvider(ctx, string(providerID))
}

func (s *service) ensureProvider(ctx context.Context, providerID string) error {
	provider, err := s.providers.GetProvider(ctx, providerID)
	if errors.Is(err, store.ErrNotFound) {
		return &providerNotFoundError{providerID: providerID}
	}
	if err != nil {
		return err
	}
	if provider == nil {
		return fmt.Errorf("provider catalog returned nil provider for %q", providerID)
	}
	return nil
}
