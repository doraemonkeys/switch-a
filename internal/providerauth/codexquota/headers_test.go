package codexquota

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestParseResponseHeaders(t *testing.T) {
	observedAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.FixedZone("SGT", 8*60*60))
	header := http.Header{
		HeaderPrimaryUsedPercent:         {"12.5"},
		HeaderPrimaryWindowMinutes:       {"300"},
		HeaderPrimaryResetAt:             {"1785819600"},
		HeaderSecondaryUsedPercent:       {"42"},
		HeaderSecondaryWindowMinutes:     {"10080"},
		HeaderSecondaryResetAfterSeconds: {"90"},
		HeaderPlanType:                   {" plus "},
	}

	snapshot, rejected := ParseResponseHeaders(header, observedAt)
	if snapshot == nil {
		t.Fatal("ParseResponseHeaders returned nil")
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected = %v, want none", rejected)
	}
	if snapshot.FetchedAt == nil || !snapshot.FetchedAt.Equal(observedAt.UTC()) {
		t.Fatalf("FetchedAt = %v, want %v", snapshot.FetchedAt, observedAt.UTC())
	}
	if snapshot.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want plus", snapshot.PlanType)
	}
	if snapshot.FiveHour == nil || snapshot.FiveHour.UsedPercent != 12.5 || snapshot.FiveHour.WindowSeconds != 5*60*60 {
		t.Fatalf("FiveHour = %#v", snapshot.FiveHour)
	}
	primaryReset := time.Unix(1785819600, 0).UTC()
	if snapshot.FiveHour.ResetAt == nil || !snapshot.FiveHour.ResetAt.Equal(primaryReset) {
		t.Fatalf("FiveHour.ResetAt = %v, want %v", snapshot.FiveHour.ResetAt, primaryReset)
	}
	secondaryReset := observedAt.Add(90 * time.Second).UTC()
	if snapshot.OneWeek == nil || snapshot.OneWeek.ResetAt == nil || !snapshot.OneWeek.ResetAt.Equal(secondaryReset) {
		t.Fatalf("OneWeek = %#v, want reset %v", snapshot.OneWeek, secondaryReset)
	}
}

func TestParseResponseHeadersUsesBengalfoxFallbackAndNamedWindowDefaults(t *testing.T) {
	observedAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	header := http.Header{
		headerBengalfoxPrimaryUsedPercent:   {"1"},
		headerBengalfoxSecondaryUsedPercent: {"2"},
		headerBengalfoxPrimaryResetAt:       {"2026-08-04T05:00:00Z"},
		headerBengalfoxPlanType:             {"team"},
	}

	snapshot, rejected := ParseResponseHeaders(header, observedAt)
	if len(rejected) != 0 {
		t.Fatalf("rejected = %v, want none", rejected)
	}
	if snapshot == nil || snapshot.PlanType != "team" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.FiveHour.WindowSeconds != defaultPrimaryWindowSeconds {
		t.Fatalf("primary window = %d", snapshot.FiveHour.WindowSeconds)
	}
	if snapshot.OneWeek.WindowSeconds != defaultSecondaryWindowSeconds {
		t.Fatalf("secondary window = %d", snapshot.OneWeek.WindowSeconds)
	}
}

func TestParseResponseHeadersKeepsValidWindowWhenRelatedFieldsAreMalformed(t *testing.T) {
	observedAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	header := http.Header{
		HeaderPrimaryUsedPercent:       {"25"},
		HeaderPrimaryWindowMinutes:     {"invalid"},
		HeaderPrimaryResetAt:           {"invalid"},
		HeaderPrimaryResetAfterSeconds: {"60"},
		HeaderSecondaryUsedPercent:     {"101"},
	}

	snapshot, rejected := ParseResponseHeaders(header, observedAt)
	if snapshot == nil || snapshot.FiveHour == nil || snapshot.OneWeek != nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if got, want := rejected, []string{HeaderPrimaryWindowMinutes, HeaderPrimaryResetAt, HeaderSecondaryUsedPercent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rejected = %v, want %v", got, want)
	}
	if snapshot.FiveHour.ResetAt == nil || !snapshot.FiveHour.ResetAt.Equal(observedAt.Add(time.Minute)) {
		t.Fatalf("ResetAt = %v", snapshot.FiveHour.ResetAt)
	}
}

func TestParseResponseHeadersRequiresUsageWindowAndObservationTime(t *testing.T) {
	if snapshot, rejected := ParseResponseHeaders(http.Header{HeaderPlanType: {"plus"}}, time.Now()); snapshot != nil || len(rejected) != 0 {
		t.Fatalf("plan-only parse = (%#v, %v), want nil", snapshot, rejected)
	}
	if snapshot, rejected := ParseResponseHeaders(http.Header{HeaderPrimaryUsedPercent: {"1"}}, time.Time{}); snapshot != nil || len(rejected) != 0 {
		t.Fatalf("zero-time parse = (%#v, %v), want nil", snapshot, rejected)
	}
	if snapshot, rejected := ParseResponseHeaders(nil, time.Now()); snapshot != nil || len(rejected) != 0 {
		t.Fatalf("nil-header parse = (%#v, %v), want nil", snapshot, rejected)
	}
}
