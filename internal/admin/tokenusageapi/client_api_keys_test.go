package tokenusageapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestClientAPIKeyFilterTransportSemantics(t *testing.T) {
	fingerprint := strings.Repeat("a", 64)
	for _, test := range []struct {
		query  string
		status int
		want   *string
	}{
		{"", http.StatusOK, nil},
		{"client_api_key_fingerprint=", http.StatusOK, new(string)},
		{"client_api_key_fingerprint=" + fingerprint, http.StatusOK, &fingerprint},
		{"client_api_key_fingerprint=abc", http.StatusBadRequest, nil},
		{"client_api_key_fingerprint=" + strings.Repeat("A", 64), http.StatusBadRequest, nil},
		{"client_api_key_fingerprint=" + strings.Repeat("g", 64), http.StatusBadRequest, nil},
		{"client_api_key_fingerprint=x&client_api_key_fingerprint=y", http.StatusBadRequest, nil},
	} {
		t.Run(test.query, func(t *testing.T) {
			analyzer := &analyzerStub{}
			handler := newTestHandler(t, analyzer, time.Now())
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage?"+test.query, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if test.status == http.StatusBadRequest {
				if analyzer.calls != 0 || !strings.Contains(response.Body.String(), clientAPIKeyFilterName) {
					t.Fatal("invalid filter was analyzed")
				}
				return
			}
			got := analyzer.query.ClientAPIKeyFingerprint
			if (got == nil) != (test.want == nil) || got != nil && *got != *test.want {
				t.Fatalf("filter = %v, want %v", got, test.want)
			}
		})
	}
}

func TestClientAPIKeyDirectoryEndpoint(t *testing.T) {
	for _, test := range []struct {
		name   string
		keys   []tokenanalytics.ClientAPIKey
		err    error
		status int
	}{
		{"empty", nil, nil, http.StatusOK},
		{"keys", []tokenanalytics.ClientAPIKey{{Fingerprint: strings.Repeat("a", 64), Name: "Laptop", MaskedKey: "prefix…tail"}}, nil, http.StatusOK},
		{"failure", nil, errors.New("private storage error"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newTestHandler(t, &analyzerStub{keys: test.keys, keysErr: test.err}, time.Now())
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/token-usage/client-api-keys", nil))
			if response.Code != test.status {
				t.Fatalf("status = %d", response.Code)
			}
			if test.err != nil {
				if strings.Contains(response.Body.String(), test.err.Error()) {
					t.Fatal("storage error exposed")
				}
				return
			}
			var keys []ClientAPIKeyDTO
			if err := json.Unmarshal(response.Body.Bytes(), &keys); err != nil {
				t.Fatal(err)
			}
			if keys == nil || len(keys) != len(test.keys) {
				t.Fatalf("keys = %+v", keys)
			}
			if len(keys) > 0 && (keys[0].Fingerprint != test.keys[0].Fingerprint || keys[0].Name != test.keys[0].Name || keys[0].MaskedKey != test.keys[0].MaskedKey) {
				t.Fatalf("keys = %+v", keys)
			}
		})
	}
}
