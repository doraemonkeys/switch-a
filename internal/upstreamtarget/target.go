// Package upstreamtarget constructs provider request targets without erasing
// client-visible URL semantics.
package upstreamtarget

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

var (
	ErrBaseURLMustBeAbsolute = errors.New("base URL must include a scheme and host")
	ErrBaseURLHasFragment    = errors.New("base URL must not include a fragment")
	ErrRequestURLRequired    = errors.New("request URL is required")
)

// ValidateBaseURL rejects fragments because HTTP transports never send them;
// accepting one would make a configured endpoint differ from the wire target.
func ValidateBaseURL(raw string) error {
	_, err := ParseBaseURL(raw)
	return err
}

// ParseBaseURL gives configuration, selection, and transport one definition of
// an address that can become an upstream wire target.
func ParseBaseURL(raw string) (*url.URL, error) {
	baseURL, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, ErrBaseURLMustBeAbsolute
	}
	if strings.Contains(raw, "#") {
		return nil, ErrBaseURLHasFragment
	}
	return baseURL, nil
}

// Build applies only the API contract's documented path rewrite. All remaining
// escaped path and raw query bytes retain their client-visible meaning.
func Build(rawBaseURL string, requestURL *url.URL, apiType string) (*url.URL, error) {
	if requestURL == nil {
		return nil, ErrRequestURLRequired
	}

	target, err := ParseBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}
	rewrittenPath, err := apicontract.RewriteUpstreamEscapedPath(requestURL.EscapedPath(), apiType)
	if err != nil {
		return nil, fmt.Errorf("rewrite upstream path: %w", err)
	}
	if err := setEscapedPath(target, joinEscapedPaths(target.EscapedPath(), rewrittenPath)); err != nil {
		return nil, err
	}

	target.RawQuery = joinRawQueries(target.RawQuery, requestURL.RawQuery)
	target.ForceQuery = target.ForceQuery || requestURL.ForceQuery
	return target, nil
}

func joinEscapedPaths(basePath, requestPath string) string {
	if basePath == "" {
		return requestPath
	}
	if requestPath == "" {
		return basePath
	}
	if strings.HasSuffix(basePath, "/") && strings.HasPrefix(requestPath, "/") {
		return basePath + requestPath[1:]
	}
	if !strings.HasSuffix(basePath, "/") && !strings.HasPrefix(requestPath, "/") {
		return basePath + "/" + requestPath
	}
	return basePath + requestPath
}

func setEscapedPath(target *url.URL, escapedPath string) error {
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return fmt.Errorf("decode upstream path: %w", err)
	}
	target.Path = decodedPath
	target.RawPath = escapedPath
	canonicalPath := (&url.URL{Path: decodedPath}).EscapedPath()
	if canonicalPath == escapedPath {
		target.RawPath = ""
	}
	return nil
}

func joinRawQueries(baseQuery, requestQuery string) string {
	if baseQuery == "" {
		return requestQuery
	}
	if requestQuery == "" {
		return baseQuery
	}
	return baseQuery + "&" + requestQuery
}
