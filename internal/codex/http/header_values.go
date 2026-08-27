package codexhttp

import (
	"net/http"
	"strings"
)

func headerValues(headers http.Header, wanted string) []string {
	var values []string
	for name, candidates := range headers {
		if strings.EqualFold(name, wanted) {
			values = append(values, candidates...)
		}
	}
	return values
}
