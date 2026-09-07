package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

func transferClientKey(id string) clientaccess.Key {
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	return clientaccess.Key{ID: id, Name: id, Key: "client-secret-" + id, CreatedAt: now, UpdatedAt: now}
}

func requestClientKeyImport(t *testing.T, handler *Handler, target *store.SQLiteStore, payload ImportConfigRequest, preview bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/api/config/import"
	if preview {
		path += "?dry_run=true"
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	revision, _ := target.InternalErrorRuleRepository().ListRules()
	request.Header.Set("If-Match", formatInternalErrorRuleETag(revision))
	result := httptest.NewRecorder()
	handler.ImportConfig(result, request)
	return result
}

func assertTransferClientSnapshot(t *testing.T, target *store.SQLiteStore, want clientaccess.Snapshot) {
	t.Helper()
	got, err := target.ClientAPIKeySnapshot(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("client snapshot = %+v, err = %v, want %+v", got, err, want)
	}
}

func TestClientAPIKeysConfigTransferRoundTripAndPreview(t *testing.T) {
	ctx := context.Background()
	source := configTransferStore(t)
	desired := clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{transferClientKey("desktop")}}
	if err := source.ClientAPIKeyRepository().Replace(ctx, desired); err != nil {
		t.Fatal(err)
	}
	exportHandler := NewHandler(Config{Store: store.NewCachedStore(store.CachedStoreConfig{Store: source}), Logger: zap.NewNop()})
	exportRecorder := httptest.NewRecorder()
	exportHandler.ExportConfig(exportRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", exportRecorder.Code, exportRecorder.Body.String())
	}
	var exported ExportedConfig
	if err := json.Unmarshal(exportRecorder.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.ClientAPIKeys == nil || !reflect.DeepEqual(*exported.ClientAPIKeys, desired) {
		t.Fatalf("portable keys = %+v", exported.ClientAPIKeys)
	}

	target := configTransferStore(t)
	handler := NewHandler(Config{Store: store.NewCachedStore(store.CachedStoreConfig{Store: target}), Logger: zap.NewNop()})
	payload := importRequestFromExport(exported)
	preview := requestClientKeyImport(t, handler, target, payload, true)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", preview.Code, preview.Body.String())
	}
	var before ImportPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Changes.ClientAPIKeys.Add != 1 || before.Changes.ClientAPIKeyPolicy == nil ||
		*before.Changes.ClientAPIKeyPolicy != (ClientAPIKeyPolicyChange{From: clientaccess.ModePermissive, To: clientaccess.ModeRestricted}) {
		t.Fatalf("preview changes = %+v", before.Changes)
	}
	assertTransferClientSnapshot(t, target, clientaccess.Snapshot{Mode: clientaccess.ModePermissive, Keys: []clientaccess.Key{}})

	applied := requestClientKeyImport(t, handler, target, payload, false)
	if applied.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", applied.Code, applied.Body.String())
	}
	var result ImportResult
	if err := json.Unmarshal(applied.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Applied.ClientAPIKeys.Added != 1 || !reflect.DeepEqual(result.Applied.ClientAPIKeyPolicy, before.Changes.ClientAPIKeyPolicy) {
		t.Fatalf("applied = %+v", result.Applied)
	}
	assertTransferClientSnapshot(t, target, desired)
	service := clientaccess.NewService(clientaccess.ServiceConfig{Store: target.ClientAPIKeyRepository()})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+desired.Keys[0].Key)
	decision, err := service.Admit(ctx, request, "openai")
	if err != nil || !decision.Allowed {
		t.Fatalf("restored key admission = %+v, %v", decision, err)
	}
}

func TestClientAPIKeysConfigTransferScopeAndEmptyReplacement(t *testing.T) {
	for _, name := range []string{"omitted", "settings_only", "selection", "empty"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			target := configTransferStore(t)
			initial := clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{transferClientKey("local")}}
			if err := target.ClientAPIKeyRepository().Replace(ctx, initial); err != nil {
				t.Fatal(err)
			}
			payload := ImportConfigRequest{Version: ConfigExportVersion, ImportScope: fullConfigImportScope(),
				ClientAPIKeys: &clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{}}}
			switch name {
			case "omitted":
				payload.ClientAPIKeys = nil
			case "settings_only":
				payload.ImportScope = &ConfigImportScope{Mode: ConfigImportModeSettingsOnly}
				payload.ClientAPIKeys.Mode = "ignored-invalid-mode"
			case "selection":
				createConfigTransferProvider(t, target, "selected")
				provider, err := target.GetProvider(ctx, "selected")
				if err != nil {
					t.Fatal(err)
				}
				sessions, err := target.ListCredentialSessions(ctx)
				if err != nil {
					t.Fatal(err)
				}
				payload.Providers = []ExportedProvider{buildExportedProvider(provider)}
				payload.CredentialSessions = []ExportedCredentialSession{buildExportedCredentialSession(&sessions[0])}
				payload.ImportScope = selectionConfigImportScope(nil, []string{provider.ID})
				payload.ClientAPIKeys.Mode = "ignored-invalid-mode"
			}
			handler := NewHandler(Config{Store: target, Logger: zap.NewNop()})
			preview := requestClientKeyImport(t, handler, target, payload, true)
			if preview.Code != http.StatusOK {
				t.Fatalf("preview = %d: %s", preview.Code, preview.Body.String())
			}
			var result ImportPreviewResponse
			if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			assertTransferClientSnapshot(t, target, initial)
			if name == "empty" {
				if result.Changes.ClientAPIKeys.Delete != 1 || result.Changes.ClientAPIKeyPolicy == nil {
					t.Fatalf("empty preview = %+v", result.Changes)
				}
			} else if result.Changes.ClientAPIKeys != (ChangeCount{}) || result.Changes.ClientAPIKeyPolicy != nil {
				t.Fatalf("out of scope changes = %+v", result.Changes)
			}
			applied := requestClientKeyImport(t, handler, target, payload, false)
			if applied.Code != http.StatusOK {
				t.Fatalf("apply = %d: %s", applied.Code, applied.Body.String())
			}
			if name == "empty" {
				initial.Keys = []clientaccess.Key{}
			}
			assertTransferClientSnapshot(t, target, initial)
		})
	}
}

func TestClientAPIKeysConfigTransferPreviewCounts(t *testing.T) {
	target := configTransferStore(t)
	initial := clientaccess.Snapshot{Mode: clientaccess.ModePermissive, Keys: []clientaccess.Key{
		transferClientKey("deleted"), transferClientKey("same"), transferClientKey("updated"),
	}}
	if err := target.ClientAPIKeyRepository().Replace(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	updated := transferClientKey("updated")
	updated.Name = "Renamed"
	desired := clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{
		updated, transferClientKey("same"), transferClientKey("added"),
	}}
	staged := stagedConfigImport{mode: ConfigImportModeFull}
	if err := stageClientAPIKeys(context.Background(), target, &ImportConfigRequest{ClientAPIKeys: &desired}, &staged); err != nil {
		t.Fatal(err)
	}
	if staged.changes.ClientAPIKeys != (ChangeCount{Add: 1, Update: 1, Delete: 1, Unchanged: 1}) {
		t.Fatalf("changes = %+v", staged.changes)
	}
	assertTransferClientSnapshot(t, target, initial)
}

func TestClientAPIKeysConfigTransferPolicyAndTimestampChanges(t *testing.T) {
	target := configTransferStore(t)
	initial := clientaccess.Snapshot{Mode: clientaccess.ModePermissive, Keys: []clientaccess.Key{transferClientKey("same")}}
	if err := target.ClientAPIKeyRepository().Replace(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	for _, timestampChange := range []bool{false, true} {
		desired := clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: append([]clientaccess.Key{}, initial.Keys...)}
		want := ChangeCount{Unchanged: 1}
		if timestampChange {
			desired.Keys[0].UpdatedAt = desired.Keys[0].UpdatedAt.Add(time.Hour)
			want = ChangeCount{Update: 1}
		}
		staged := stagedConfigImport{mode: ConfigImportModeFull}
		if err := stageClientAPIKeys(context.Background(), target, &ImportConfigRequest{ClientAPIKeys: &desired}, &staged); err != nil {
			t.Fatal(err)
		}
		if staged.changes.ClientAPIKeys != want || staged.changes.ClientAPIKeyPolicy == nil ||
			staged.changes.ClientAPIKeyPolicy.To != clientaccess.ModeRestricted || staged.bundle.ClientAPIKeys == nil {
			t.Fatalf("policy/timestamp preview = %+v", staged.changes)
		}
	}
	assertTransferClientSnapshot(t, target, initial)
}

func TestClientAPIKeysConfigTransferRejectsMalformedAggregate(t *testing.T) {
	key := transferClientKey("a")
	duplicateValue := transferClientKey("b")
	duplicateValue.Key = key.Key
	badKey := transferClientKey("bad")
	badKey.Key = "bad\r\nvalue"
	for _, desired := range []clientaccess.Snapshot{
		{Mode: "unknown"},
		{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{key, key}},
		{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{key, duplicateValue}},
		{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{badKey}},
	} {
		target := configTransferStore(t)
		handler := NewHandler(Config{Store: target, Logger: zap.NewNop()})
		payload := ImportConfigRequest{Version: ConfigExportVersion, ClientAPIKeys: &desired}
		for _, preview := range []bool{true, false} {
			result := requestClientKeyImport(t, handler, target, payload, preview)
			if result.Code != http.StatusBadRequest {
				t.Fatalf("malformed aggregate preview=%t status=%d body=%s", preview, result.Code, result.Body.String())
			}
			assertTransferClientSnapshot(t, target, clientaccess.Snapshot{Mode: clientaccess.ModePermissive, Keys: []clientaccess.Key{}})
		}
	}
}

type failingClientKeyTransferStore struct {
	Store
	err error
}

func (s failingClientKeyTransferStore) ClientAPIKeySnapshot(context.Context) (clientaccess.Snapshot, error) {
	return clientaccess.Snapshot{}, s.err
}

func TestClientAPIKeysConfigTransferReadFailure(t *testing.T) {
	target := configTransferStore(t)
	broken := failingClientKeyTransferStore{Store: target, err: errors.Join(errors.New("corrupt stored snapshot"), clientaccess.ErrValidation)}
	handler := NewHandler(Config{Store: broken, Logger: zap.NewNop()})
	export := httptest.NewRecorder()
	handler.ExportConfig(export, httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil))
	if export.Code != http.StatusInternalServerError {
		t.Fatalf("export = %d", export.Code)
	}
	payload := ImportConfigRequest{Version: ConfigExportVersion, ClientAPIKeys: &clientaccess.Snapshot{Mode: clientaccess.ModeRestricted}}
	result := requestClientKeyImport(t, handler, target, payload, true)
	if result.Code != http.StatusInternalServerError {
		t.Fatalf("preview = %d: %s", result.Code, result.Body.String())
	}
	staged := stagedConfigImport{mode: ConfigImportModeFull}
	if err := stageClientAPIKeys(context.Background(), struct{}{}, &payload, &staged); err == nil {
		t.Fatal("missing snapshot source accepted an access-policy import")
	}
}
