package capturebridge

import (
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

var sensitiveHeaderNames = [...]string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Set-Cookie",
	"X-API-Key",
	"API-Key",
	"X-Goog-API-Key",
	"X-Access-Token",
	"X-Amz-Credential",
	"X-Amz-Security-Token",
	"X-Auth-Token",
	"X-Goog-Credential",
	"ChatGPT-Account-Id",
}

func CredentialMaterial(headers http.Header) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	var sensitiveHeaders requestcapture.SensitiveHeaderEvidence
	var evidence requestcapture.CredentialEvidence
	for _, name := range sensitiveHeaderNames {
		sensitiveHeaders.Add(name)
	}
	// Header.Values canonicalizes its lookup key and can miss valid values in a
	// directly constructed map. Scanning actual entries keeps producer evidence
	// complete for both wire-decoded and programmatically assembled requests.
	for actualName, headerValues := range headers {
		name, sensitive := knownSensitiveHeaderName(actualName)
		if !sensitive {
			continue
		}
		for _, value := range headerValues {
			captureCredentialValue(&evidence, name, value)
			if evidence.Overflowed() {
				break
			}
		}
		if evidence.Overflowed() {
			break
		}
	}
	// Sealing distinguishes a completed empty scan from missing evidence. That
	// distinction lets sanitization retain safe diagnostics while failing closed
	// whenever an integration did not complete its credential scan.
	sensitiveHeaders.Seal()
	evidence.Seal()
	return sensitiveHeaders, evidence
}

func knownSensitiveHeaderName(actual string) (string, bool) {
	actual = strings.TrimSpace(actual)
	for _, expected := range sensitiveHeaderNames {
		if len(actual) != len(expected) {
			continue
		}
		matched := true
		for index := 0; index < len(actual); index++ {
			actualByte := actual[index]
			expectedByte := expected[index]
			if actualByte == '_' {
				actualByte = '-'
			}
			if actualByte >= 'A' && actualByte <= 'Z' {
				actualByte += 'a' - 'A'
			}
			if expectedByte >= 'A' && expectedByte <= 'Z' {
				expectedByte += 'a' - 'A'
			}
			if actualByte != expectedByte {
				matched = false
				break
			}
		}
		if matched {
			return expected, true
		}
	}
	return "", false
}

func captureCredentialValue(
	evidence *requestcapture.CredentialEvidence,
	headerName string,
	value string,
) {
	trimmed := strings.TrimSpace(value)
	evidence.Add(trimmed)
	if evidence.Overflowed() || trimmed == "" {
		return
	}
	switch headerName {
	case "Authorization", "Proxy-Authorization":
		if separator := strings.IndexAny(trimmed, " \t"); separator >= 0 {
			evidence.Add(strings.TrimSpace(trimmed[separator+1:]))
		}
	case "Cookie":
		captureCookieCredentialValues(evidence, trimmed, false)
	case "Set-Cookie":
		captureCookieCredentialValues(evidence, trimmed, true)
	}
}

func captureCookieCredentialValues(
	evidence *requestcapture.CredentialEvidence,
	value string,
	firstOnly bool,
) {
	for offset := 0; offset <= len(value); {
		end := strings.IndexByte(value[offset:], ';')
		if end < 0 {
			end = len(value)
		} else {
			end += offset
		}
		part := strings.TrimSpace(value[offset:end])
		separator := strings.IndexByte(part, '=')
		component := part
		if separator > 0 {
			component = strings.TrimSpace(part[separator+1:])
			if len(component) >= 2 && component[0] == '"' && component[len(component)-1] == '"' {
				component = component[1 : len(component)-1]
			}
		}
		evidence.Add(component)
		if evidence.Overflowed() || firstOnly || end == len(value) {
			return
		}
		offset = end + 1
	}
}
