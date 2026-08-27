// Package clientcredential extracts the client credential that scopes Codex
// continuity and cookie state without depending on a wire transport.
package clientcredential

import (
	"crypto/subtle"
	"strings"
)

const MaxClientCredentialBytes = 8192

type State string

const (
	StateAbsent    State = "absent"
	StateSingle    State = "single"
	StateInvalid   State = "invalid"
	StateAmbiguous State = "ambiguous"
)

type Result struct {
	State State
	Token []byte
}

// Clear releases the only plaintext copy owned by the extraction result.
func (r *Result) Clear() {
	if r == nil {
		return
	}
	clear(r.Token)
	r.Token = nil
}

// Extract applies one credential grammar to every transport adapter. Header
// names remain case-insensitive even when a non-HTTP transport supplies the map.
func Extract(headers map[string][]string) Result {
	authorization := headerValues(headers, "Authorization")
	apiKeys := headerValues(headers, "X-Api-Key")
	if len(authorization) == 0 && len(apiKeys) == 0 {
		return Result{State: StateAbsent}
	}

	var bearer []byte
	if len(authorization) > 0 {
		if len(authorization) != 1 {
			return Result{State: StateInvalid}
		}
		var valid bool
		bearer, valid = parseBearer(authorization[0])
		if !valid {
			return Result{State: StateInvalid}
		}
	}

	var apiKey []byte
	if len(apiKeys) > 0 {
		if len(apiKeys) != 1 {
			clear(bearer)
			return Result{State: StateInvalid}
		}
		value := trimOptionalWhitespace(apiKeys[0])
		if value == "" || len(value) > MaxClientCredentialBytes {
			clear(bearer)
			return Result{State: StateInvalid}
		}
		apiKey = []byte(value)
	}

	if len(bearer) > 0 && len(apiKey) > 0 {
		if !equalCredentialBytes(bearer, apiKey) {
			clear(bearer)
			clear(apiKey)
			return Result{State: StateAmbiguous}
		}
		clear(apiKey)
		return Result{State: StateSingle, Token: bearer}
	}
	if len(bearer) > 0 {
		return Result{State: StateSingle, Token: bearer}
	}
	return Result{State: StateSingle, Token: apiKey}
}

func headerValues(headers map[string][]string, wanted string) []string {
	var values []string
	for name, candidates := range headers {
		if strings.EqualFold(name, wanted) {
			values = append(values, candidates...)
		}
	}
	return values
}

func parseBearer(value string) ([]byte, bool) {
	const scheme = "Bearer"
	if len(value) < len(scheme) || !strings.EqualFold(value[:len(scheme)], scheme) {
		return nil, false
	}
	remainder := value[len(scheme):]
	if remainder == "" || !isOptionalWhitespace(remainder[0]) {
		return nil, false
	}
	token := trimOptionalWhitespace(remainder)
	if token == "" || len(token) > MaxClientCredentialBytes {
		return nil, false
	}
	return []byte(token), true
}

func equalCredentialBytes(left, right []byte) bool {
	var leftPadded [MaxClientCredentialBytes]byte
	var rightPadded [MaxClientCredentialBytes]byte
	copy(leftPadded[:], left)
	copy(rightPadded[:], right)
	equalContent := subtle.ConstantTimeCompare(leftPadded[:], rightPadded[:])
	equalLength := subtle.ConstantTimeEq(int32(len(left)), int32(len(right)))
	clear(leftPadded[:])
	clear(rightPadded[:])
	return equalContent&equalLength == 1
}

func trimOptionalWhitespace(value string) string {
	return strings.Trim(value, " \t")
}

func isOptionalWhitespace(value byte) bool {
	return value == ' ' || value == '\t'
}
