package proxy

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestX3ForwardResultIsFactOnly(t *testing.T) {
	t.Parallel()

	facts := reflect.TypeOf(forwardResult{})
	if facts.Kind() != reflect.Struct {
		t.Fatalf("forwardResult kind = %s, want struct", facts.Kind())
	}
	bannedCapabilities := []reflect.Type{
		reflect.TypeOf((*error)(nil)).Elem(),
		reflect.TypeOf((*io.ReadCloser)(nil)).Elem(),
		reflect.TypeOf((*http.ResponseWriter)(nil)).Elem(),
		reflect.TypeOf((*providerLease)(nil)).Elem(),
		reflect.TypeOf((*retryPermit)(nil)).Elem(),
		reflect.TypeOf((*alternateProviderReservation)(nil)).Elem(),
	}
	allowedPointers := map[string]bool{"firstTokenMs": true, "tokenUsage": true, "semantic": true}
	for index := 0; index < facts.NumField(); index++ {
		field := facts.Field(index)
		if field.Anonymous {
			t.Fatalf("forwardResult embeds %s; facts must remain explicit", field.Type)
		}
		if field.Type.Kind() == reflect.Chan || field.Type.Kind() == reflect.Func || field.Type.Kind() == reflect.Interface {
			t.Fatalf("forwardResult.%s retains live capability kind %s", field.Name, field.Type.Kind())
		}
		if field.Type.Kind() == reflect.Pointer && !allowedPointers[field.Name] {
			t.Fatalf("forwardResult.%s is unexpected pointer %s", field.Name, field.Type)
		}
		for _, capability := range bannedCapabilities {
			if field.Type == capability || field.Type.Implements(capability) {
				t.Fatalf("forwardResult.%s retains capability %s", field.Name, capability)
			}
		}
		name := field.Type.String()
		if strings.Contains(name, "PendingResponse") || strings.Contains(name, "upstreamtransport.Response") {
			t.Fatalf("forwardResult.%s retains response owner %s", field.Name, name)
		}
	}

	owner := reflect.TypeOf(pendingHTTPResponse{})
	pendingOwners := 0
	for index := 0; index < owner.NumField(); index++ {
		field := owner.Field(index)
		if field.Type == reflect.TypeOf((*responseanalysis.PendingResponse)(nil)) {
			pendingOwners++
		}
		if field.Type == reflect.TypeOf((*upstreamtransport.Response)(nil)) || field.Type.Implements(reflect.TypeOf((*io.ReadCloser)(nil)).Elem()) {
			t.Fatalf("pendingHTTPResponse.%s bypasses the sole PendingResponse owner", field.Name)
		}
	}
	if pendingOwners != 1 {
		t.Fatalf("pendingHTTPResponse owner count = %d, want 1", pendingOwners)
	}
}
