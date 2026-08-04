package errorruleapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
)

const ruleSetETagPrefix = "internal-error-rules/"

func FormatRuleSetETag(revision errorrule.Revision) string {
	return `"` + ruleSetETagPrefix + revision.String() + `"`
}

func ParseRuleSetETag(raw string) (errorrule.Revision, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' ||
		strings.Contains(raw, ",") || strings.HasPrefix(raw, "W/") || raw == "*" {
		return 0, fmt.Errorf("must contain exactly one strong internal-error rule ETag")
	}
	payload := raw[1 : len(raw)-1]
	if !strings.HasPrefix(payload, ruleSetETagPrefix) {
		return 0, fmt.Errorf("is not an internal-error rule ETag")
	}
	revisionText := strings.TrimPrefix(payload, ruleSetETagPrefix)
	revision, err := strconv.ParseInt(revisionText, 10, 64)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != revisionText {
		return 0, fmt.Errorf("contains an invalid rule revision")
	}
	return errorrule.Revision(revision), nil
}

func parseIfMatch(header http.Header, required bool) (*errorrule.Revision, *apiError) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		if !required {
			return nil, nil
		}
		return nil, &apiError{
			Status: http.StatusPreconditionRequired, Code: ErrorCodePreconditionRequired,
			Message: "If-Match is required", Details: map[string]any{},
		}
	}
	if len(values) != 1 {
		return nil, validationError("If-Match", "If-Match must contain exactly one strong internal-error rule ETag", nil)
	}
	revision, err := ParseRuleSetETag(values[0])
	if err != nil {
		return nil, validationError("If-Match", "If-Match "+err.Error(), err)
	}
	return &revision, nil
}
