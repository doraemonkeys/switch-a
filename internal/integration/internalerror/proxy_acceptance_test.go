package internalerror_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"
)

func TestV5BHTTPAndSSEActionTable(t *testing.T) {
	protocols := []struct {
		name        string
		contentType string
		errorWire   []byte
		successWire []byte
	}{
		{
			name:        "json",
			contentType: "application/json",
			errorWire:   []byte(`{"type":"error","error":{"message":"overloaded"}}`),
			successWire: []byte(`{"id":"v5b-success","output":[]}`),
		},
		{
			name:        "sse",
			contentType: "text/event-stream",
			errorWire:   []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"overloaded\"}\r\n\r\n"),
			successWire: []byte("event: response.completed\r\ndata: {\"type\":\"response.completed\"}\r\n\r\n"),
		},
	}
	actions := []struct {
		name             string
		action           func(*testing.T) errorrule.Action
		wantBody         func(errorWire, successWire []byte) []byte
		wantAttempts     int
		wantPrimaryCalls int
		wantSecondCalls  int
		wantDecision     errorrule.DecisionValue
		wantVerdict      model.RequestAttemptHealthVerdict
		wantCause        model.RequestAttemptHealthCause
		wantVisible      bool
		wantTermination  requestcapture.TerminationReason
	}{
		{
			name:             "passthrough",
			action:           func(*testing.T) errorrule.Action { return errorrule.NewPassthroughAction() },
			wantBody:         func(errorWire, _ []byte) []byte { return errorWire },
			wantAttempts:     1,
			wantPrimaryCalls: 1,
			wantDecision:     errorrule.DecisionPassthrough,
			wantVerdict:      model.RequestAttemptHealthNeutral,
			wantCause:        model.RequestAttemptHealthCauseSemanticNeutral,
			wantVisible:      true,
			wantTermination:  requestcapture.TerminationReasonInternalErrorCommitted,
		},
		{
			name: "retry_only",
			action: func(t *testing.T) errorrule.Action {
				return retryOnlyAction(t, 1)
			},
			wantBody:         func(_, successWire []byte) []byte { return successWire },
			wantAttempts:     2,
			wantPrimaryCalls: 2,
			wantDecision:     errorrule.DecisionRetrySame,
			wantVerdict:      model.RequestAttemptHealthNeutral,
			wantCause:        model.RequestAttemptHealthCauseSemanticNeutral,
			wantTermination:  requestcapture.TerminationReasonInternalErrorAbsorbed,
		},
		{
			name: "retry_then_switch",
			action: func(t *testing.T) errorrule.Action {
				return retryThenSwitchAction(t, 0)
			},
			wantBody:         func(_, successWire []byte) []byte { return successWire },
			wantAttempts:     2,
			wantPrimaryCalls: 1,
			wantSecondCalls:  1,
			wantDecision:     errorrule.DecisionSwitchProvider,
			wantVerdict:      model.RequestAttemptHealthFailure,
			wantCause:        model.RequestAttemptHealthCauseSemanticRetryThenSwitch,
			wantTermination:  requestcapture.TerminationReasonInternalErrorAbsorbed,
		},
	}

	for _, protocol := range protocols {
		for _, actionCase := range actions {
			t.Run(protocol.name+"/"+actionCase.name, func(t *testing.T) {
				primaryResponses := []wireResponse{{
					status: http.StatusOK, contentType: protocol.contentType, body: protocol.errorWire,
				}}
				if actionCase.name == "retry_only" {
					primaryResponses = append(primaryResponses, wireResponse{
						status: http.StatusOK, contentType: protocol.contentType, body: protocol.successWire,
					})
				}
				primary := newUpstreamSequence(t, primaryResponses...)
				var secondary *upstreamSequence
				if actionCase.name == "retry_then_switch" {
					secondary = newUpstreamSequence(t, wireResponse{
						status: http.StatusOK, contentType: protocol.contentType, body: protocol.successWire,
					})
				}
				harness := newProxyHarness(t, proxyHarnessOptions{
					action: actionCase.action(t), globalMaxAttempts: 4,
					primary: primary, secondary: secondary, capture: true,
				})

				recorder := harness.serve(t)
				wantBody := actionCase.wantBody(protocol.errorWire, protocol.successWire)
				if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), wantBody) {
					t.Fatalf("response status=%d body=%q, want status=200 body=%q", recorder.Code, recorder.Body.Bytes(), wantBody)
				}
				if primary.CallCount() != actionCase.wantPrimaryCalls {
					t.Fatalf("primary calls=%d, want %d", primary.CallCount(), actionCase.wantPrimaryCalls)
				}
				if secondary != nil && secondary.CallCount() != actionCase.wantSecondCalls {
					t.Fatalf("secondary calls=%d, want %d", secondary.CallCount(), actionCase.wantSecondCalls)
				}
				if harness.ruleSetReads.calls.Load() != 1 {
					t.Fatalf("rule-set snapshot reads=%d, want one pinned read", harness.ruleSetReads.calls.Load())
				}

				attempts := harness.attempts(t)
				if len(attempts) != actionCase.wantAttempts {
					t.Fatalf("attempts=%#v, want %d", attempts, actionCase.wantAttempts)
				}
				assertAttemptAxes(
					t, attempts[0], model.RequestAttemptOutcomeUpstreamSemanticError,
					actionCase.wantVisible, actionCase.wantVerdict, actionCase.wantCause,
				)
				semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
				if semantic.Retry.Action != actionCase.action(t).Type() || semantic.Decision.Value != actionCase.wantDecision {
					t.Fatalf("semantic action/decision = %q/%q", semantic.Retry.Action, semantic.Decision.Value)
				}
				if semantic.Rule.WinnerID != harness.rule.ID || len(semantic.Rule.MatchingRuleIDs) != 1 {
					t.Fatalf("semantic rule facts = %#v", semantic.Rule)
				}
				if actionCase.wantAttempts == 2 {
					assertAttemptAxes(
						t, attempts[1], model.RequestAttemptOutcomeUpstreamCompleted,
						true, model.RequestAttemptHealthSuccess, model.RequestAttemptHealthCauseNormalCompletion,
					)
				}
				if actionCase.name == "retry_then_switch" {
					if attempts[1].ProviderID != secondaryProviderID ||
						attempts[1].SwitchMode != model.RequestAttemptSwitchModeReplacement ||
						attempts[0].SwitchReason != string(errorrule.SwitchReasonRuleExhausted) {
						t.Fatalf(
							"replacement fields second_provider=%q second_mode=%q first_reason=%q",
							attempts[1].ProviderID, attempts[1].SwitchMode, attempts[0].SwitchReason,
						)
					}
					if semantic.Alternate.Outcome != "activated" || semantic.Alternate.ProviderID == nil ||
						*semantic.Alternate.ProviderID != secondaryProviderID {
						t.Fatalf("alternate evidence = %#v", semantic.Alternate)
					}
				}

				if err := harness.stats.Flush(context.Background()); err != nil {
					t.Fatalf("flush rule statistics: %v", err)
				}
				_, stats, err := harness.store.InternalErrorRuleRepository().ListStatsSnapshot(context.Background())
				if err != nil || len(stats) != 1 || stats[0].HitCount != 1 {
					t.Fatalf("rule statistics=%#v err=%v", stats, err)
				}

				page := harness.capturePage(t)
				if len(page.Records) != actionCase.wantAttempts {
					t.Fatalf("capture records=%#v", page.Records)
				}
				sort.Slice(page.Records, func(i, j int) bool {
					return page.Records[i].ExchangeIndex < page.Records[j].ExchangeIndex
				})
				first := page.Records[0]
				wantWritten := int64(0)
				if actionCase.wantVisible {
					wantWritten = int64(len(protocol.errorWire))
				}
				if first.TerminationReason != actionCase.wantTermination ||
					first.UpstreamObservedBytes != int64(len(protocol.errorWire)) ||
					first.ApplicationWriteConfirmedBytes != wantWritten || !first.HasFailure ||
					first.Failure.Primary.Class != requestcapture.FailureClassUpstreamSemantic ||
					first.Failure.Primary.Code != requestcapture.FailureCodeProviderSemantic {
					t.Fatalf("first capture record = %#v", first)
				}
				detail := harness.captureDetail(t, first.RecordID)
				if detail.Summary.RecordID != first.RecordID || detail.HTTP == nil ||
					detail.HTTP.ResponseBody.CapturedBytes != int64(len(protocol.errorWire)) ||
					detail.HTTP.ResponseBody.ChecksumSHA256 == "" {
					t.Fatalf("first capture detail = %#v", detail)
				}
			})
		}
	}
}

func TestV5BFinalAttemptSlotIsReservedForAlternate(t *testing.T) {
	errorWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"overloaded\"}\r\n\r\n")
	successWire := []byte(`{"id":"alternate-success"}`)
	primary := newUpstreamSequence(t,
		wireResponse{contentType: "text/event-stream", body: errorWire},
		wireResponse{contentType: "application/json", body: []byte(`{"id":"wrong-same-provider"}`)},
	)
	secondary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: successWire})
	harness := newProxyHarness(t, proxyHarnessOptions{
		action: retryThenSwitchAction(t, 3), globalMaxAttempts: 2,
		primary: primary, secondary: secondary,
	})

	recorder := harness.serve(t)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) {
		t.Fatalf("response status=%d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	if primary.CallCount() != 1 || secondary.CallCount() != 1 {
		t.Fatalf("upstream calls primary=%d secondary=%d", primary.CallCount(), secondary.CallCount())
	}
	attempts := harness.attempts(t)
	if len(attempts) != 2 || attempts[0].ProviderID != primaryProviderID || attempts[1].ProviderID != secondaryProviderID {
		t.Fatalf("attempt chain = %#v", attempts)
	}
	semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
	if semantic.Decision.Value != errorrule.DecisionSwitchProvider ||
		semantic.Decision.Reason != errorrule.ReasonReservedSwitchAttempt ||
		semantic.Retry.RuleRetriesScheduled != "0" || semantic.Alternate.Outcome != "activated" {
		t.Fatalf("last-slot evidence = %#v", semantic)
	}
}

func TestV5BFinalAttemptSlotWithoutAlternateDoesNotChargeRetry(t *testing.T) {
	errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
	primary := newUpstreamSequence(t,
		wireResponse{contentType: "application/json", body: errorWire},
		wireResponse{contentType: "application/json", body: []byte(`{"id":"must-not-run"}`)},
	)
	harness := newProxyHarness(t, proxyHarnessOptions{
		action: retryThenSwitchAction(t, 3), globalMaxAttempts: 2, primary: primary,
	})

	recorder := harness.serve(t)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), errorWire) || primary.CallCount() != 1 {
		t.Fatalf("response status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
	}
	attempts := harness.attempts(t)
	if len(attempts) != 1 {
		t.Fatalf("attempts=%#v", attempts)
	}
	if attempts[0].AttemptEvidenceJSON == nil {
		t.Fatal("[V5B-B02] alternate-unavailable final-slot path dropped semantic attempt evidence")
	}
	semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
	if semantic.Retry.RuleRetriesScheduled != "0" ||
		semantic.Decision.Value != errorrule.DecisionCommitCurrent ||
		semantic.Decision.Reason != errorrule.ReasonAlternateProviderUnavailable ||
		semantic.Alternate.Outcome != "unavailable" {
		t.Fatalf("last-slot no-alternate evidence=%#v", semantic)
	}
}

func TestV5BRetryOnlyExhaustionCommitsExactHTTPAndSSEWire(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		wire        []byte
	}{
		{
			name: "json", contentType: "application/json",
			wire: []byte(`{"type":"error","error":{"message":"overloaded"}}`),
		},
		{
			name: "sse", contentType: "text/event-stream",
			wire: []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"overloaded\"}\r\n\r\n"),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			primary := newUpstreamSequence(t, wireResponse{contentType: testCase.contentType, body: testCase.wire})
			harness := newProxyHarness(t, proxyHarnessOptions{
				action: retryOnlyAction(t, 1), globalMaxAttempts: 1, primary: primary,
			})
			recorder := harness.serve(t)
			if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), testCase.wire) || primary.CallCount() != 1 {
				t.Fatalf("response status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
			}
			attempts := harness.attempts(t)
			assertAttemptAxes(t, attempts[0], model.RequestAttemptOutcomeUpstreamSemanticError, true,
				model.RequestAttemptHealthNeutral, model.RequestAttemptHealthCauseSemanticNeutral)
			if attempts[0].AttemptEvidenceJSON == nil {
				t.Fatal("[V5B-B02] exhausted retry-only terminal path dropped semantic attempt evidence")
			}
			semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
			if semantic.Decision.Value != errorrule.DecisionCommitCurrent ||
				semantic.Decision.Reason != errorrule.ReasonGlobalAttemptBudgetExhausted {
				t.Fatalf("retry exhaustion evidence=%#v", semantic.Decision)
			}
		})
	}
}

func TestV5BMixedLegacyAndRuleRetryLedgers(t *testing.T) {
	statusWire := []byte(`{"error":"temporary status"}`)
	semanticWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
	successWire := []byte(`{"id":"mixed-ledger-success"}`)
	primary := newUpstreamSequence(t,
		wireResponse{status: http.StatusInternalServerError, contentType: "application/json", body: statusWire},
		wireResponse{status: http.StatusOK, contentType: "application/json", body: semanticWire},
		wireResponse{status: http.StatusOK, contentType: "application/json", body: successWire},
	)
	health := newRecordingHealth(false)
	harness := newProxyHarness(t, proxyHarnessOptions{
		action: retryOnlyAction(t, 1), globalMaxAttempts: 3,
		primary: primary, primaryMaxRetries: 1, health: health,
	})

	recorder := harness.serve(t)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) || primary.CallCount() != 3 {
		t.Fatalf("response status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
	}
	attempts := harness.attempts(t)
	if len(attempts) != 3 {
		t.Fatalf("attempt chain = %#v", attempts)
	}
	assertAttemptAxes(t, attempts[0], model.RequestAttemptOutcomeUpstreamHTTPStatusError, false,
		model.RequestAttemptHealthFailure, model.RequestAttemptHealthCauseHTTPStatusFailure)
	assertAttemptAxes(t, attempts[1], model.RequestAttemptOutcomeUpstreamSemanticError, false,
		model.RequestAttemptHealthNeutral, model.RequestAttemptHealthCauseSemanticNeutral)
	assertAttemptAxes(t, attempts[2], model.RequestAttemptOutcomeUpstreamCompleted, true,
		model.RequestAttemptHealthSuccess, model.RequestAttemptHealthCauseNormalCompletion)
	semantic := decodeSemanticEvidence(t, attempts[1].AttemptEvidenceJSON)
	if semantic.Retry.GlobalAttemptsStarted != "2" || semantic.Retry.RuleRetriesScheduled != "0" ||
		semantic.Decision.Value != errorrule.DecisionRetrySame {
		t.Fatalf("mixed-ledger evidence = %#v", semantic.Retry)
	}
}

func TestV5BRuleSnapshotIsPinnedAcrossConcurrentMutation(t *testing.T) {
	errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
	successWire := []byte(`{"id":"pinned-success"}`)
	var mutate func()
	primary := newUpstreamSequence(t,
		wireResponse{
			contentType: "application/json", body: errorWire,
			beforeWrite: func() { mutate() },
		},
		wireResponse{contentType: "application/json", body: successWire},
	)
	harness := newProxyHarness(t, proxyHarnessOptions{
		action: retryOnlyAction(t, 1), globalMaxAttempts: 2, primary: primary,
	})
	mutate = func() {
		spec := harness.rule.RuleSpec
		spec.Action = errorrule.NewPassthroughAction()
		if _, err := harness.store.InternalErrorRuleRepository().UpdateRule(
			context.Background(), 1, harness.rule.ID, spec,
		); err != nil {
			panic(err)
		}
	}

	recorder := harness.serve(t)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) || primary.CallCount() != 2 {
		t.Fatalf("pinned request status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
	}
	if harness.ruleSetReads.calls.Load() != 1 {
		t.Fatalf("rule-set reads=%d, want exactly one", harness.ruleSetReads.calls.Load())
	}
	if revision, _ := harness.store.InternalErrorRuleRepository().ListRules(); revision != 2 {
		t.Fatalf("live revision=%d, want concurrent mutation revision 2", revision)
	}
}

func TestV5BApprovedRetrySurvivesCircuitButNotProviderDeletion(t *testing.T) {
	t.Run("circuit", func(t *testing.T) {
		errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
		successWire := []byte(`{"id":"circuit-retry-success"}`)
		primary := newUpstreamSequence(t,
			wireResponse{contentType: "application/json", body: errorWire},
			wireResponse{contentType: "application/json", body: successWire},
		)
		health := newRecordingHealth(true)
		harness := newProxyHarness(t, proxyHarnessOptions{
			action: retryThenSwitchAction(t, 1), globalMaxAttempts: 3,
			primary: primary, health: health, ruleProviderID: primaryProviderID,
		})

		recorder := harness.serve(t)
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) || primary.CallCount() != 2 {
			t.Fatalf("response status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
		}
		if health.FailureCount(primaryProviderID) != 1 || health.SuccessCount(primaryProviderID) != 1 {
			t.Fatalf("health failures=%d successes=%d", health.FailureCount(primaryProviderID), health.SuccessCount(primaryProviderID))
		}
		semantic := decodeSemanticEvidence(t, harness.attempts(t)[0].AttemptEvidenceJSON)
		if !semantic.Health.CircuitOpened || semantic.Decision.Value != errorrule.DecisionRetrySame {
			t.Fatalf("circuit retry evidence = %#v", semantic)
		}
	})

	t.Run("deletion", func(t *testing.T) {
		errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
		primary := newUpstreamSequence(t,
			wireResponse{contentType: "application/json", body: errorWire},
			wireResponse{contentType: "application/json", body: []byte(`{"id":"must-not-run"}`)},
		)
		health := newRecordingHealth(true)
		harness := newProxyHarness(t, proxyHarnessOptions{
			action: retryThenSwitchAction(t, 1), globalMaxAttempts: 3,
			primary: primary, health: health, ruleProviderID: primaryProviderID,
		})
		var deleteErr error
		health.onFailure = func(ctx context.Context, providerID string) {
			deleteErr = harness.store.DeleteProvider(ctx, providerID)
		}

		recorder := harness.serve(t)
		if deleteErr != nil {
			t.Fatalf("delete provider at retry boundary: %v", deleteErr)
		}
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), errorWire) || primary.CallCount() != 1 {
			t.Fatalf("response status=%d body=%q calls=%d", recorder.Code, recorder.Body.Bytes(), primary.CallCount())
		}
		attempts := harness.attempts(t)
		if len(attempts) != 1 || attempts[0].ResultVisibleToClient == nil || !*attempts[0].ResultVisibleToClient {
			t.Fatalf("deleted-provider attempts = %#v", attempts)
		}
		revision, rules := harness.store.InternalErrorRuleRepository().ListRules()
		if revision != 2 || len(rules) != 0 {
			t.Fatalf("post-delete revision=%d rules=%#v", revision, rules)
		}
		if attempts[0].AttemptEvidenceJSON == nil {
			t.Fatal("[V5B-B02] provider-deletion retry invalidation dropped semantic attempt evidence")
		}
		semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
		if semantic.Decision.Value != errorrule.DecisionCommitCurrent ||
			semantic.Decision.Reason != errorrule.ReasonAlternateProviderUnavailable {
			t.Fatalf("deleted-provider evidence = %#v", semantic.Decision)
		}
	})
}

func TestV5BContinuitySeedPromotesReplacementToFailover(t *testing.T) {
	errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
	successWire := []byte(`{"id":"continuity-success"}`)
	primary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: errorWire})
	secondary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: successWire})
	seeds := proxy.NewVisibleContinuitySeedStore()
	key := selector.BuildContinuityKey(&model.SelectRequest{
		ClientIP: acceptanceClientIP, User: acceptanceUser, APIType: proxy.APITypeCodex,
		Model: acceptanceModel, StickyMode: model.StickyModeModel,
	})
	seeds.Store(model.VisibleContinuitySeed{
		SeedID: "v5b-seed", ContinuityKey: key,
		OriginProviderID: primaryProviderID, OriginVendor: "v5b-vendor",
		ContaminatedVendors: []string{"v5b-vendor"}, StrictestScope: model.ScopeAny,
		ObservedAt: time.Now().Add(-time.Second),
	})
	sticky := selector.NewMemoryStickyCache(internal.RealClock{})
	sticky.Set(key, primaryProviderID, time.Minute)
	harness := newProxyHarness(t, proxyHarnessOptions{
		action: retryThenSwitchAction(t, 0), globalMaxAttempts: 2,
		primary: primary, secondary: secondary,
		stickyMode: model.StickyModeModel, stickyCache: sticky, continuitySeeds: seeds,
	})

	recorder := harness.serve(t)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), successWire) {
		t.Fatalf("response status=%d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	attempts := harness.attempts(t)
	if seeds.Len() != 0 {
		logs, err := harness.store.ListLogs(context.Background(), model.LogFilter{Limit: 1})
		var clientIP, user, requestModel string
		if len(logs) != 0 {
			clientIP, user, requestModel = logs[0].ClientIP, logs[0].UserID, logs[0].Model
		}
		t.Fatalf(
			"continuity seed count=%d attempt_count=%d first_seeded=%t first_origin=%q second_mode=%q log_key=%q/%q/%q err=%v",
			seeds.Len(), len(attempts), attempts[0].ContinuitySeeded, attempts[0].ContinuityOriginProviderID,
			attempts[1].SwitchMode, clientIP, user, requestModel, err,
		)
	}
	if len(attempts) != 2 || !attempts[0].ContinuitySeeded ||
		attempts[0].ContinuityOriginProviderID != primaryProviderID ||
		attempts[0].ContinuitySeedAgeMs == nil ||
		attempts[1].SwitchMode != model.RequestAttemptSwitchModeFailover ||
		attempts[1].ContinuityOriginProviderID != primaryProviderID {
		t.Fatalf("continuity attempt chain = %#v", attempts)
	}
	semantic := decodeSemanticEvidence(t, attempts[0].AttemptEvidenceJSON)
	if semantic.Alternate.SwitchMode == nil || *semantic.Alternate.SwitchMode != "failover" {
		t.Fatalf("continuity alternate evidence = %#v", semantic.Alternate)
	}
}

func TestV5BLateSemanticObservationPreservesHealthFacts(t *testing.T) {
	errorWire := []byte(`{"type":"error","error":{"message":"overloaded"}}`)
	successWire := []byte(`{"id":"inside-window-success"}`)
	insidePrimary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: errorWire})
	insideSecondary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: successWire})
	inside := newProxyHarness(t, proxyHarnessOptions{
		action: retryThenSwitchAction(t, 0), globalMaxAttempts: 2,
		primary: insidePrimary, secondary: insideSecondary,
	})
	insideRecorder := inside.serve(t)
	if !bytes.Equal(insideRecorder.Body.Bytes(), successWire) {
		t.Fatalf("inside-window body=%q", insideRecorder.Body.Bytes())
	}
	insideAttempt := inside.attempts(t)[0]
	insideSemantic := decodeSemanticEvidence(t, insideAttempt.AttemptEvidenceJSON)

	scheduler := newControlledScheduler()
	releaseBody := make(chan struct{})
	delayedPrimary := &upstreamSequence{}
	delayedPrimary.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delayedPrimary.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-releaseBody
		_, _ = w.Write(errorWire)
	}))
	t.Cleanup(delayedPrimary.server.Close)
	outsideSecondary := newUpstreamSequence(t, wireResponse{contentType: "application/json", body: successWire})
	outside := newProxyHarness(t, proxyHarnessOptions{
		action: retryThenSwitchAction(t, 0), globalMaxAttempts: 2,
		primary: delayedPrimary, secondary: outsideSecondary,
		analysisScheduler: scheduler, analysisProbeLimit: time.Hour,
	})
	recorderResult := make(chan *httptest.ResponseRecorder, 1)
	go func() { recorderResult <- outside.serve(t) }()

	var scheduled scheduledCall
	select {
	case scheduled = <-scheduler.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("response analysis did not schedule its probe boundary")
	}
	if !scheduled.Fire() {
		t.Fatal("probe boundary was already resolved")
	}
	close(releaseBody)
	var outsideRecorder *httptest.ResponseRecorder
	select {
	case outsideRecorder = <-recorderResult:
	case <-time.After(5 * time.Second):
		t.Fatal("late-observation request did not complete")
	}
	if !bytes.Equal(outsideRecorder.Body.Bytes(), errorWire) || outsideSecondary.CallCount() != 0 {
		t.Fatalf("outside-window body=%q secondary calls=%d", outsideRecorder.Body.Bytes(), outsideSecondary.CallCount())
	}
	outAttempts := outside.attempts(t)
	if len(outAttempts) != 1 {
		t.Fatalf("outside-window attempts=%#v", outAttempts)
	}
	outAttempt := outAttempts[0]
	outSemantic := decodeSemanticEvidence(t, outAttempt.AttemptEvidenceJSON)
	if insideAttempt.HealthVerdict == nil || outAttempt.HealthVerdict == nil ||
		*insideAttempt.HealthVerdict != *outAttempt.HealthVerdict ||
		insideAttempt.HealthCause == nil || outAttempt.HealthCause == nil ||
		*insideAttempt.HealthCause != *outAttempt.HealthCause ||
		*outAttempt.HealthVerdict != model.RequestAttemptHealthFailure ||
		*outAttempt.HealthCause != model.RequestAttemptHealthCauseSemanticRetryThenSwitch {
		t.Fatalf("inside/outside health axes inside=%#v outside=%#v", insideAttempt, outAttempt)
	}
	if insideSemantic.Health != outSemantic.Health {
		t.Fatalf("inside/outside semantic health inside=%#v outside=%#v", insideSemantic.Health, outSemantic.Health)
	}
	if outSemantic.Response.BoundaryReason != responseanalysis.BoundaryProbeDurationElapsed ||
		outSemantic.Decision.Value != errorrule.DecisionObserveOnly ||
		outSemantic.Decision.Reason != errorrule.ReasonResponseAlreadyVisible ||
		!outSemantic.Response.VisibleToClient {
		t.Fatalf("late semantic evidence = %#v", outSemantic)
	}
}

func retryOnlyAction(t *testing.T, maxRetries int) errorrule.Action {
	t.Helper()
	action, err := errorrule.NewRetryOnlyAction(maxRetries, model.BackoffPolicy{})
	if err != nil {
		t.Fatalf("create retry-only action: %v", err)
	}
	return action
}

func retryThenSwitchAction(t *testing.T, maxRetries int) errorrule.Action {
	t.Helper()
	action, err := errorrule.NewRetryThenSwitchAction(maxRetries, model.BackoffPolicy{})
	if err != nil {
		t.Fatalf("create retry-then-switch action: %v", err)
	}
	return action
}
