package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/store"
)

func TestCodexFeatureConfigKeyContract(t *testing.T) {
	defaults := store.GetDefaultConfigs()
	for _, key := range codexstartup.Keys() {
		if !IsValidConfigKey(key) {
			t.Errorf("IsValidConfigKey(%q) = false", key)
		}
		if defaults[key] != "false" {
			t.Errorf("default %q = %q, want false", key, defaults[key])
		}
		if err := ValidateConfigValue(key, "true"); err != nil {
			t.Errorf("ValidateConfigValue(%q, true) error = %v", key, err)
		}
		if err := ValidateConfigValue(key, "invalid"); err == nil {
			t.Errorf("ValidateConfigValue(%q, invalid) succeeded", key)
		}
	}
}

func TestUpdateConfigCoversAllCodexFeatureCombinations(t *testing.T) {
	for bits := 0; bits < 16; bits++ {
		bits := bits
		t.Run(fmt.Sprintf("combination_%04b", bits), func(t *testing.T) {
			h, st, _ := testHandler()
			updates := map[string]string{
				codexstartup.KeyUpstreamHeaderHygiene: fmt.Sprint(bits&1 != 0),
				codexstartup.KeyWebSocketSubprotocol:  fmt.Sprint(bits&2 != 0),
				codexstartup.KeyContinuity:            fmt.Sprint(bits&4 != 0),
				codexstartup.KeyProviderCookieJar:     fmt.Sprint(bits&8 != 0),
			}
			body, err := json.Marshal(updates)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/admin/api/config", bytes.NewReader(body))
			response := httptest.NewRecorder()
			h.UpdateConfig(response, request)

			wantRejected := bits&1 == 0 && (bits&4 != 0 || bits&8 != 0)
			if wantRejected {
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
				}
				for key := range updates {
					if _, persisted := st.config[key]; persisted {
						t.Errorf("invalid combination persisted %q", key)
					}
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
			}
			for key, value := range updates {
				if st.config[key] != value {
					t.Errorf("persisted %q = %q, want %q", key, st.config[key], value)
				}
			}
		})
	}
}

func TestUpdateConfigRejectsDisablingRequiredHygiene(t *testing.T) {
	h, st, _ := testHandler()
	st.config[codexstartup.KeyUpstreamHeaderHygiene] = "true"
	st.config[codexstartup.KeyContinuity] = "true"

	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/config",
		bytes.NewBufferString(`{"codex_upstream_header_hygiene_enabled":"false"}`),
	)
	response := httptest.NewRecorder()
	h.UpdateConfig(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if st.config[codexstartup.KeyUpstreamHeaderHygiene] != "true" {
		t.Fatal("rejected update changed the durable hygiene flag")
	}
}

func TestUpdateConfigFailsClosedWhenCapabilityValidationFails(t *testing.T) {
	h, st, _ := testHandler()
	validationFailure := errors.New("capability repository unavailable")
	validator := &recordingCodexFeatureValidator{err: validationFailure}
	h.codexFeatureValidator = validator

	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/config",
		bytes.NewBufferString(`{"codex_upstream_header_hygiene_enabled":"true"}`),
	)
	response := httptest.NewRecorder()
	h.UpdateConfig(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if len(validator.snapshots) != 1 || !validator.snapshots[0].UpstreamHeaderHygiene {
		t.Fatalf("validated snapshots = %+v", validator.snapshots)
	}
	if _, persisted := st.config[codexstartup.KeyUpstreamHeaderHygiene]; persisted {
		t.Fatal("configuration persisted after capability validation failure")
	}
}

func TestCodexFeatureMutationsFailClosedWhenSnapshotReadFails(t *testing.T) {
	h, st, _ := testHandler()
	readFailure := errors.New("config snapshot unavailable")
	h.store = &codexFeatureConfigReadErrorStore{mockStore: st, err: readFailure}

	err := h.applyValidatedConfigUpdate(context.Background(), map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
	})
	if !errors.Is(err, readFailure) {
		t.Fatalf("applyValidatedConfigUpdate() error = %v, want %v", err, readFailure)
	}

	err = h.applyValidatedConfigImport(
		context.Background(),
		ImportChanges{},
		&store.ConfigImportBundle{Settings: map[string]string{
			codexstartup.KeyUpstreamHeaderHygiene: "true",
		}},
	)
	if !errors.Is(err, readFailure) {
		t.Fatalf("applyValidatedConfigImport() error = %v, want %v", err, readFailure)
	}
}

func TestCodexFeatureUpdatePublishesOnlyAfterDurablePersistence(t *testing.T) {
	h, st, _ := testHandler()
	publisher := &recordingCodexFeatureValidator{
		onPublish: func(snapshot codexstartup.Snapshot) error {
			if st.config[codexstartup.KeyUpstreamHeaderHygiene] != "true" {
				t.Fatal("feature snapshot published before configuration was durable")
			}
			if !snapshot.UpstreamHeaderHygiene {
				t.Fatalf("published snapshot = %+v", snapshot)
			}
			return nil
		},
	}
	h.codexFeatureValidator = publisher

	err := h.applyValidatedConfigUpdate(context.Background(), map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
	})
	if err != nil {
		t.Fatalf("applyValidatedConfigUpdate() error = %v", err)
	}
	if len(publisher.snapshots) != 1 || len(publisher.published) != 1 {
		t.Fatalf("validated=%+v published=%+v", publisher.snapshots, publisher.published)
	}
}

func TestCodexFeatureUpdateDoesNotPublishWhenPersistenceFails(t *testing.T) {
	h, st, _ := testHandler()
	publisher := &recordingCodexFeatureValidator{}
	h.codexFeatureValidator = publisher
	wantErr := errors.New("write unavailable")
	h.store = &codexFeatureConfigWriteErrorStore{mockStore: st, err: wantErr}

	err := h.applyValidatedConfigUpdate(context.Background(), map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("applyValidatedConfigUpdate() error = %v, want %v", err, wantErr)
	}
	if len(publisher.snapshots) != 1 || len(publisher.published) != 0 {
		t.Fatalf("validated=%+v published=%+v", publisher.snapshots, publisher.published)
	}
}

func TestCodexFeaturePublishFailureKeepsDurableUpdateRecoverable(t *testing.T) {
	h, st, _ := testHandler()
	wantErr := errors.New("publisher unavailable")
	publisher := &recordingCodexFeatureValidator{publishErr: wantErr}
	h.codexFeatureValidator = publisher
	update := map[string]string{codexstartup.KeyUpstreamHeaderHygiene: "true"}

	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/config",
		bytes.NewBufferString(`{"codex_upstream_header_hygiene_enabled":"true"}`),
	)
	response := httptest.NewRecorder()
	h.UpdateConfig(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("publish failure status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if st.config[codexstartup.KeyUpstreamHeaderHygiene] != "true" || len(publisher.published) != 0 {
		t.Fatalf("failed publish durable=%q published=%+v", st.config[codexstartup.KeyUpstreamHeaderHygiene], publisher.published)
	}
	publisher.publishErr = nil
	if err := h.applyValidatedConfigUpdate(context.Background(), update); err != nil {
		t.Fatalf("retry applyValidatedConfigUpdate() error = %v", err)
	}
	if len(publisher.published) != 1 || !publisher.published[0].UpstreamHeaderHygiene {
		t.Fatalf("retry published snapshots = %+v", publisher.published)
	}
}

func TestCodexFeatureValidationErrorStatusMapping(t *testing.T) {
	h, _, _ := testHandler()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name: "missing compiled capability",
			err: func() error {
				return (codexstartup.Snapshot{UpstreamHeaderHygiene: true}).Validate(
					codexstartup.CompiledCapabilities{},
					nil,
					codexstartup.ReferencedKeyVersions{},
				)
			}(),
			wantStatus: http.StatusBadRequest,
		},
		{name: "canceled validation", err: context.Canceled, wantStatus: http.StatusRequestTimeout},
		{name: "repository failure", err: errors.New("repository failure"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.writeCodexFeatureConfigError(response, test.err)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestUpdateConfigRejectsMalformedStoredFeatureState(t *testing.T) {
	h, st, _ := testHandler()
	st.config[codexstartup.KeyContinuity] = "malformed"
	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/config",
		bytes.NewBufferString(`{"log_retention_days":"9"}`),
	)
	response := httptest.NewRecorder()
	h.UpdateConfig(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if _, persisted := st.config["log_retention_days"]; persisted {
		t.Fatal("unrelated update persisted while feature state was malformed")
	}
}

func TestCodexFeatureConfigExportImportMapping(t *testing.T) {
	t.Run("export", func(t *testing.T) {
		h, st, _ := testHandler()
		for _, key := range codexstartup.Keys() {
			st.config[key] = "false"
		}
		st.config[codexstartup.KeyUpstreamHeaderHygiene] = "true"
		st.config[codexstartup.KeyWebSocketSubprotocol] = "true"

		request := httptest.NewRequest(http.MethodGet, "/admin/api/config/export", nil)
		response := httptest.NewRecorder()
		h.ExportConfig(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
		}
		var exported ExportedConfig
		if err := json.NewDecoder(response.Body).Decode(&exported); err != nil {
			t.Fatal(err)
		}
		for _, key := range codexstartup.Keys() {
			if _, exists := exported.Settings[key]; !exists {
				t.Errorf("export omitted %q", key)
			}
		}
	})

	t.Run("settings import", func(t *testing.T) {
		h, st, _ := testHandler()
		publisher := &recordingCodexFeatureValidator{}
		h.codexFeatureValidator = publisher
		body := `{"version":"5.0","import_scope":{"mode":"settings_only"},"providers":[],"credential_sessions":[],"groups":[],"routing_policies":[],"settings":{"codex_upstream_header_hygiene_enabled":"true","codex_provider_cookie_jar_enabled":"true"},"internal_error_rules":[]}`
		request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		h.ImportConfig(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
		}
		if st.config[codexstartup.KeyUpstreamHeaderHygiene] != "true" ||
			st.config[codexstartup.KeyProviderCookieJar] != "true" {
			t.Fatalf("imported settings = %v", st.config)
		}
		if len(publisher.published) != 1 || !publisher.published[0].ProviderCookieJar {
			t.Fatalf("published imported snapshots = %+v", publisher.published)
		}
	})

	t.Run("dry-run validates without persistence", func(t *testing.T) {
		h, st, _ := testHandler()
		publisher := &recordingCodexFeatureValidator{}
		h.codexFeatureValidator = publisher
		body := `{"version":"5.0","import_scope":{"mode":"settings_only"},"providers":[],"credential_sessions":[],"groups":[],"routing_policies":[],"settings":{"codex_upstream_header_hygiene_enabled":"true","codex_continuity_enabled":"true"},"internal_error_rules":[]}`
		request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		h.ImportConfig(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
		}
		if len(st.config) != 0 {
			t.Fatalf("dry-run persisted settings: %v", st.config)
		}
		if len(publisher.snapshots) != 1 || len(publisher.published) != 0 {
			t.Fatalf("dry-run validated=%+v published=%+v", publisher.snapshots, publisher.published)
		}
	})

	t.Run("invalid dependency dry-run", func(t *testing.T) {
		h, st, _ := testHandler()
		body := `{"version":"5.0","import_scope":{"mode":"settings_only"},"providers":[],"credential_sessions":[],"groups":[],"routing_policies":[],"settings":{"codex_continuity_enabled":"true"},"internal_error_rules":[]}`
		request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import?dry_run=true", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		h.ImportConfig(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
		}
		if len(st.config) != 0 {
			t.Fatalf("invalid dry-run persisted settings: %v", st.config)
		}
	})

	t.Run("invalid dependency import", func(t *testing.T) {
		h, st, _ := testHandler()
		body := `{"version":"5.0","import_scope":{"mode":"settings_only"},"providers":[],"credential_sessions":[],"groups":[],"routing_policies":[],"settings":{"codex_continuity_enabled":"true"},"internal_error_rules":[]}`
		request := httptest.NewRequest(http.MethodPost, "/admin/api/config/import", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		h.ImportConfig(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
		}
		if _, persisted := st.config[codexstartup.KeyContinuity]; persisted {
			t.Fatal("invalid imported feature combination was persisted")
		}
	})
}

type recordingCodexFeatureValidator struct {
	snapshots  []codexstartup.Snapshot
	published  []codexstartup.Snapshot
	err        error
	publishErr error
	onPublish  func(codexstartup.Snapshot) error
}

type codexFeatureConfigReadErrorStore struct {
	*mockStore
	err error
}

type codexFeatureConfigWriteErrorStore struct {
	*mockStore
	err error
}

func (st *codexFeatureConfigReadErrorStore) GetAllConfig(context.Context) (map[string]string, error) {
	return nil, st.err
}

func (validator *recordingCodexFeatureValidator) ValidateCodexFeatures(
	_ context.Context,
	snapshot codexstartup.Snapshot,
) error {
	validator.snapshots = append(validator.snapshots, snapshot)
	return validator.err
}

func (validator *recordingCodexFeatureValidator) PublishCodexFeatures(snapshot codexstartup.Snapshot) error {
	if validator.publishErr != nil {
		return validator.publishErr
	}
	if validator.onPublish != nil {
		if err := validator.onPublish(snapshot); err != nil {
			return err
		}
	}
	validator.published = append(validator.published, snapshot)
	return nil
}

func (st *codexFeatureConfigWriteErrorStore) SetConfigs(context.Context, map[string]string) error {
	return st.err
}
