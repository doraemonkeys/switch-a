package tokenanalytics

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

func TestServiceAssessesQualityOnlyWithObservedUsage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		summary SummaryRecord
		want    float64
	}{
		{name: "no requests"},
		{name: "requests without usage", summary: SummaryRecord{TotalRequests: 3}},
		{
			name: "all observations non-comparable",
			summary: SummaryRecord{
				TotalRequests: 3, ObservedRequests: 3,
				PartialRequests: 1, InvalidRequests: 1, UnknownSemanticsRequests: 1,
			},
		},
		{
			name: "comparable zero-token usage",
			summary: SummaryRecord{
				TotalRequests: 3, ObservedRequests: 3, ComparableRequests: 3,
			},
			want: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
			service := NewService(&fakeReader{snapshot: &fakeSnapshot{summary: test.summary}})
			report, err := service.Analyze(context.Background(), Query{Window: analyticswindow.Window{
				Period: analyticswindow.Period24Hours, GranularityName: analyticswindow.Granularity1Hour,
				Granularity: time.Hour, Start: start, End: start.Add(time.Hour),
			}})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			if test.summary.ObservedRequests == 0 {
				if report.DataQuality.QualityRate != nil {
					t.Fatalf("unassessed quality = %v, want nil", *report.DataQuality.QualityRate)
				}
				return
			}
			if report.DataQuality.QualityRate == nil {
				t.Fatal("observed usage quality must be assessed")
			}
			if got := *report.DataQuality.QualityRate; got != test.want {
				t.Fatalf("quality = %v, want %v", got, test.want)
			}
		})
	}
}
