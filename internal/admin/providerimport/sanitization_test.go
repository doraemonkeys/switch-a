package providerimport

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

func TestBuildProviderImportPreviewResponseBoundsSourceControlledDisplayText(t *testing.T) {
	overlong := strings.Repeat("界", 2_000)
	preview := &providerauth.ChatGPTProviderImportPreview{
		Items: []providerauth.ChatGPTProviderImportPreviewItem{{
			CandidateID: "candidate",
			State:       providerauth.ChatGPTProviderImportCandidateStateInvalid,
			Name:        overlong,
			Auth: &providerauth.ProviderAuthView{
				Email:     overlong,
				AccountID: overlong,
				PlanType:  overlong,
			},
			Warnings: []providerauth.ChatGPTProviderImportWarning{{Code: overlong, Message: overlong}},
		}},
		Warnings: []providerauth.ChatGPTProviderImportWarning{{Code: overlong, Message: overlong}},
	}

	response, _ := buildProviderImportPreviewResponse(preview, nil)
	item := response.Items[0]
	assertProviderImportTextBound(t, "name", item.Name, maxProviderImportNameCharacters)
	assertProviderImportTextBound(t, "email", item.Email, maxProviderImportEmailCharacters)
	assertProviderImportTextBound(t, "account ID", item.AccountID, maxProviderImportAccountIDCharacters)
	assertProviderImportTextBound(t, "plan type", item.PlanType, maxProviderImportPlanTypeCharacters)
	assertProviderImportTextBound(t, "message", item.Message, maxProviderImportMessageCharacters)
	assertProviderImportTextBound(t, "warning code", item.Warnings[0].Code, maxProviderImportWarningCodeCharacters)
	assertProviderImportTextBound(t, "warning message", item.Warnings[0].Message, maxProviderImportMessageCharacters)
	assertProviderImportTextBound(t, "global warning code", response.Warnings[0].Code, maxProviderImportWarningCodeCharacters)
	assertProviderImportTextBound(t, "global warning message", response.Warnings[0].Message, maxProviderImportMessageCharacters)
}

func assertProviderImportTextBound(t *testing.T, field, value string, limit int) {
	t.Helper()
	if count := utf8.RuneCountInString(value); count > limit {
		t.Fatalf("%s has %d characters, want at most %d", field, count, limit)
	}
}
