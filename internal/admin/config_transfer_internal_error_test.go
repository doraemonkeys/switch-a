package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func configTransferStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	result, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "config.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Close() })
	return result
}

func createConfigTransferProvider(t *testing.T, target *store.SQLiteStore, id string) {
	t.Helper()
	session := &credentialsession.Session{
		ID: id + "-session", Kind: credentialsession.KindAPIKey, SecretData: "secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	if _, err := target.CreateCredentialSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	provider := &model.Provider{
		ID: id, Name: "Provider", AuthMode: "bearer", Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id, APIType: "codex", BaseURL: "https://example.com",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: id, APIType: "codex", Credential: credentialsession.Snapshot{SessionID: session.ID},
		}},
	}
	if err := target.CreateProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
}

func createConfigTransferRule(t *testing.T, target *store.SQLiteStore, id, providerID string) errorrule.Rule {
	t.Helper()
	ruleTarget, _ := errorrule.NewProviderTarget(errorrule.ProviderID(providerID))
	action, err := errorrule.NewRetryOnlyActionWithVisibleResponse(
		1,
		model.BackoffPolicy{},
		errorrule.VisibleResponseCommit,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := errorrulesqlite.ImportRequest{
		Mode: errorrulesqlite.ImportModeFull,
		Rules: []errorrulesqlite.ImportedRule{{
			ID: errorrule.RuleID(id),
			RuleSpec: errorrule.RuleSpec{
				Name: "Capacity", Enabled: true, Target: ruleTarget,
				Keywords: []string{"overloaded"}, MatchMode: errorrule.MatchAny,
				Action: action,
			},
		}},
	}
	result, err := target.InternalErrorRuleRepository().Coordinate(
		context.Background(), nil,
		func(_ *gorm.DB, current []errorrule.Rule) ([]errorrule.Rule, error) {
			candidate, _, err := errorrulesqlite.BuildImportCandidate(current, request)
			return candidate, err
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result.Rules[0]
}

func TestConfigTransferV5RoundTripIncludesRulesButNotStats(t *testing.T) {
	source := configTransferStore(t)
	createConfigTransferProvider(t, source, "provider-a")
	rule := createConfigTransferRule(t, source, "11111111-1111-4111-8111-111111111111", "provider-a")
	if _, err := source.InternalErrorRuleRepository().ApplyRuleStatDeltas(context.Background(), []statistics.Delta{{
		Handle: statistics.HandleFor(rule), HitCount: 9, LastHitAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	exportHandler := NewHandler(Config{Store: source, Logger: zap.NewNop()})
	exportRecorder := httptest.NewRecorder()
	exportHandler.ExportConfig(exportRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	var exported ExportedConfig
	if err := json.Unmarshal(exportRecorder.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Version != ConfigExportVersion || len(exported.InternalErrorRules) != 1 || exported.InternalErrorRules[0].ID != rule.ID {
		t.Fatalf("exported=%#v", exported)
	}
	if len(exported.CredentialSessions) != 1 ||
		exported.CredentialSessions[0].TransferMode != CredentialSessionTransferStaticSecret {
		t.Fatalf("static credential transfer = %#v", exported.CredentialSessions)
	}
	rawRule, _ := json.Marshal(exported.InternalErrorRules[0])
	for _, forbidden := range []string{"position", "created_at", "updated_at", "generation", "hit_count", "last_hit_at"} {
		if strings.Contains(string(rawRule), forbidden) {
			t.Fatalf("exported rule contains %q: %s", forbidden, rawRule)
		}
	}

	destination := configTransferStore(t)
	importRequest := ImportConfigRequest{
		Version: ConfigExportVersion, ImportScope: fullConfigImportScope(),
		Providers: exported.Providers, Groups: exported.Groups,
		CredentialSessions: exported.CredentialSessions,
		RoutingPolicies:    exported.RoutingPolicies, Settings: exported.Settings,
		InternalErrorRules: exported.InternalErrorRules,
	}
	body, _ := json.Marshal(importRequest)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	request.Header.Set("If-Match", formatInternalErrorRuleETag(0))
	recorder := httptest.NewRecorder()
	NewHandler(Config{Store: destination, Logger: zap.NewNop()}).ImportConfig(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result ImportResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Applied.InternalErrorRules.Added != 1 || result.RuleSetRevision != "1" || result.RuleSetETag != formatInternalErrorRuleETag(1) {
		t.Fatalf("result=%#v", result)
	}
	revision, imported := destination.InternalErrorRuleRepository().ListRules()
	if revision != 1 || len(imported) != 1 || imported[0].ID != rule.ID {
		t.Fatalf("revision=%d imported=%#v", revision, imported)
	}
	if imported[0].Action.VisibleResponsePolicy() != errorrule.VisibleResponseCommit {
		t.Fatalf("imported visible response policy = %q", imported[0].Action.VisibleResponsePolicy())
	}
	provider, err := destination.GetProvider(context.Background(), "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := provider.CredentialSessionForAPIType("codex")
	if !ok || credential.SessionID != exported.CredentialSessions[0].ID || credential.SecretData != exported.CredentialSessions[0].SecretData {
		t.Fatalf("static provider/session round-trip = %#v", provider)
	}
	stats, err := destination.InternalErrorRuleRepository().ListStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].HitCount != 0 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
}

func TestConfigImportPreviewReturnsRuleRevisionAndActualRequiresETag(t *testing.T) {
	target := configTransferStore(t)
	handler := NewHandler(Config{Store: target, Logger: zap.NewNop()})
	requestBody := ImportConfigRequest{
		Version: ConfigExportVersion, ImportScope: fullConfigImportScope(),
		Providers: []ExportedProvider{}, Groups: []ExportedGroup{},
		RoutingPolicies: []ExportedRoutingPolicy{}, Settings: map[string]string{},
		InternalErrorRules: []ExportedInternalErrorRule{},
	}
	body, _ := json.Marshal(requestBody)

	preview := httptest.NewRecorder()
	handler.ImportConfig(preview, httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewReader(body)))
	if preview.Code != http.StatusOK || preview.Header().Get("ETag") != formatInternalErrorRuleETag(0) {
		t.Fatalf("preview status=%d etag=%q body=%s", preview.Code, preview.Header().Get("ETag"), preview.Body.String())
	}
	var previewBody ImportPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil || previewBody.RuleSetRevision != "0" {
		t.Fatalf("preview body=%#v err=%v", previewBody, err)
	}

	missing := httptest.NewRecorder()
	handler.ImportConfig(missing, httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body)))
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d body=%s", missing.Code, missing.Body.String())
	}
	staleRequest := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(body))
	staleRequest.Header.Set("If-Match", formatInternalErrorRuleETag(1))
	stale := httptest.NewRecorder()
	handler.ImportConfig(stale, staleRequest)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body.String())
	}

	oversized := requestBody
	oversized.InternalErrorRules = make([]ExportedInternalErrorRule, errorrule.MaxRuleCount+1)
	for index := range oversized.InternalErrorRules {
		oversized.InternalErrorRules[index] = ExportedInternalErrorRule{
			ID: errorrule.RuleID(fmt.Sprintf("00000000-0000-4000-8000-%012d", index)),
			RuleSpec: errorrule.RuleSpec{
				Name: "capacity", Enabled: true, Target: errorrule.NewGlobalTarget(),
				Keywords: []string{"capacity"}, MatchMode: errorrule.MatchAny,
				Action: errorrule.NewPassthroughAction(),
			},
		}
	}
	oversizedBody, err := json.Marshal(oversized)
	if err != nil {
		t.Fatal(err)
	}
	capacity := httptest.NewRecorder()
	handler.ImportConfig(capacity, httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewReader(oversizedBody)))
	if capacity.Code != http.StatusConflict {
		t.Fatalf("capacity status=%d body=%s", capacity.Code, capacity.Body.String())
	}
}
