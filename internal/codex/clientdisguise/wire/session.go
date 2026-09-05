// Package wire derives target-specific bytes while keeping original protocol
// evidence and replay sources owned by their existing runtime boundaries.
package wire

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"github.com/google/uuid"
)

const snippetLimit = 512

type Mapper interface {
	MapIdentity(context.Context, disguise.MappingKey) (string, error)
	RestoreIdentity(context.Context, string, string, string, string) (string, bool, error)
}

type Failure struct {
	DiagnosticID    string `json:"diagnostic_id"`
	OperationID     string `json:"operation_id"`
	Stage           string `json:"stage"`
	Carrier         string `json:"carrier"`
	FieldPath       string `json:"field_path"`
	OriginalSnippet string `json:"original_snippet"`
	DerivedSnippet  string `json:"derived_snippet"`
	Cause           error  `json:"-"`
}

func (f *Failure) Error() string {
	return fmt.Sprintf("client disguise transformation failed [%s] at %s %s: %v", f.DiagnosticID, f.Stage, f.FieldPath, f.Cause)
}
func (f *Failure) Unwrap() error { return f.Cause }

type Difference struct {
	Carrier   string `json:"carrier"`
	FieldPath string `json:"field_path"`
	Original  string `json:"original"`
	Derived   string `json:"derived"`
}

type Session struct {
	mapper          Mapper
	target          disguise.TargetSnapshot
	clientID        string
	operationID     string
	mu              sync.Mutex
	installations   map[string]string
	differences     []Difference
	terminalFailure *Failure
}

func NewSession(mapper Mapper, target disguise.TargetSnapshot, clientIdentityID, operationID string) *Session {
	target.Profile.Features.Headers = cloneMap(target.Profile.Features.Headers)
	target.Binding.TelemetryPathMappings = cloneMap(target.Binding.TelemetryPathMappings)
	if target.Transport != nil {
		transport := *target.Transport
		transport.Config = append([]byte(nil), transport.Config...)
		target.Transport = &transport
	}
	return &Session{mapper: mapper, target: target, clientID: clientIdentityID, operationID: operationID, installations: make(map[string]string)}
}
func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}
func (s *Session) Differences() []Difference {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Difference(nil), s.differences...)
}
func (s *Session) failure(stage, carrier, path, original, derived string, cause error) *Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalFailure == nil {
		s.terminalFailure = &Failure{DiagnosticID: uuid.NewString(), OperationID: s.operationID, Stage: stage, Carrier: carrier, FieldPath: path, OriginalSnippet: snippet(original), DerivedSnippet: snippet(derived), Cause: cause}
	}
	return s.terminalFailure
}
func (s *Session) Failure() *Failure { s.mu.Lock(); defer s.mu.Unlock(); return s.terminalFailure }
func snippet(value string) string {
	if len(value) > snippetLimit {
		return value[:snippetLimit]
	}
	return value
}
func (s *Session) difference(carrier, path, original, derived string) {
	if original == derived {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Replays can repeat the same decision indefinitely; evidence describes fields,
	// not transmission counts (which the sending layer already records).
	for _, d := range s.differences {
		if d.Carrier == carrier && d.FieldPath == path && d.Original == snippet(original) && d.Derived == snippet(derived) {
			return
		}
	}
	s.differences = append(s.differences, Difference{carrier, path, snippet(original), snippet(derived)})
}
func (s *Session) TransportConfig() (upstreamtransport.WireConfig, error) {
	if !s.target.Policy.Enabled || s.target.Transport == nil {
		return upstreamtransport.WireConfig{}, nil
	}
	config, err := upstreamtransport.ParseWireConfig(s.target.Transport.Config)
	if err != nil {
		return config, s.failure("transport", "transport", "$.config", string(s.target.Transport.Config), "", err)
	}
	return config, nil
}

// WebSocket upgrades require HTTP/1 support in the sampled transport; choosing
// a different protocol silently would misrepresent the selected sample.
func (s *Session) WebSocketTransportConfig() (upstreamtransport.WireConfig, error) {
	config, err := s.TransportConfig()
	if err != nil {
		return config, err
	}
	hasHTTP2 := config.HTTPProtocol == "http2"
	for _, protocol := range config.ALPN {
		if protocol == "h2" {
			hasHTTP2 = true
		}
	}
	if hasHTTP2 {
		return config, s.failure("transport", "websocket", "$.config.http_protocol", "", "", fmt.Errorf("sampled HTTP/2 negotiation is unsupported by the HTTP/1 WebSocket upgrade engine"))
	}
	return config, nil
}
func (s *Session) identity(ctx context.Context, namespace, value string, restore bool) (string, error) {
	if value == "" {
		return value, nil
	}
	switch namespace {
	case "installation":
		return s.installationIdentity(value, restore)
	case "window":
		if thread, generation, ok := windowParts(value); ok {
			mapped, err := s.identity(ctx, "thread", thread, restore)
			return mapped + generation, err
		}
	}
	if s.mapper == nil {
		return "", fmt.Errorf("identity mapper is unavailable")
	}
	if !restore {
		return s.mapper.MapIdentity(ctx, disguise.MappingKey{GenerationID: s.target.Login.GenerationID, ClientIdentityID: s.clientID, Namespace: namespace, Original: value})
	}
	original, ok, err := s.mapper.RestoreIdentity(ctx, s.target.Login.GenerationID, s.clientID, namespace, value)
	if err != nil {
		return "", err
	}
	if ok {
		return original, nil
	}
	return value, nil
}
func (s *Session) installationIdentity(value string, restore bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if restore {
		if original, ok := s.installations[value]; ok {
			return original, nil
		}
		return value, nil
	}
	if s.target.Login.DeviceID == "" {
		return "", fmt.Errorf("login device identity is missing")
	}
	// The virtual device is login-scoped; its inverse observation remains local
	// because a login can represent more than one downstream installation.
	if _, exists := s.installations[s.target.Login.DeviceID]; !exists {
		s.installations[s.target.Login.DeviceID] = value
	}
	return s.target.Login.DeviceID, nil
}
func windowParts(value string) (string, string, bool) {
	split := strings.LastIndexByte(value, ':')
	if split <= 0 || split >= len(value)-1 {
		return "", "", false
	}
	for _, c := range value[split+1:] {
		if c < '0' || c > '9' {
			return "", "", false
		}
	}
	return value[:split], value[split:], true
}
func (s *Session) Headers(ctx context.Context, original http.Header) (http.Header, error) {
	return s.headers(ctx, original, false)
}
func (s *Session) RestoreHeaders(ctx context.Context, original http.Header) (http.Header, error) {
	return s.headers(ctx, original, true)
}
func (s *Session) headers(ctx context.Context, original http.Header, restore bool) (http.Header, error) {
	result := original.Clone()
	if !s.target.Policy.Enabled {
		return result, nil
	}
	if result == nil {
		result = make(http.Header)
	}
	for name, values := range result {
		kind := fieldKind(strings.ToLower(name))
		if kind == "" && !restore {
			kind = headerFeatureKind(name)
		}
		if kind == "" {
			continue
		}
		for index, value := range values {
			derived, err := s.transformValue(ctx, kind, value, restore, "header", name)
			if err != nil {
				return nil, err
			}
			result[name][index] = derived
		}
	}
	if !restore {
		features := s.target.Profile.Features
		updates := cloneMap(features.Headers)
		if updates == nil {
			updates = make(map[string]string)
		}
		if features.UserAgent != "" {
			updates["User-Agent"] = features.UserAgent
		}
		if features.Originator != "" {
			updates["Originator"] = features.Originator
		}
		for name, value := range updates {
			if !featureHeader(name) {
				continue
			}
			old := result.Get(name)
			result.Set(name, value)
			s.difference("header", name, old, value)
		}
	}
	return result, nil
}
func featureHeader(name string) bool {
	switch strings.ToLower(name) {
	case "user-agent", "originator", "version", "x-client-version", "x-codex-client-version", "x-codex-desktop-build", "x-codex-os-version", "x-stainless-os", "x-stainless-arch", "x-stainless-package-version", "x-stainless-runtime-version":
		return true
	default:
		return false
	}
}
func fieldKind(name string) string {
	switch strings.ToLower(name) {
	case "installation_id", "installation-id", "x-codex-installation-id":
		return "installation"
	case "session_id", "session-id":
		return "session"
	case "thread_id", "thread-id":
		return "thread"
	case "turn_id", "turn-id", "x-codex-turn-id":
		return "turn"
	case "request_id", "request-id", "x-client-request-id":
		return "request"
	case "window_id", "window-id", "x-codex-window-id":
		return "window"
	case "prompt_cache_key":
		return "cache"
	case "x-codex-turn-metadata", "turn_metadata":
		return "serialized"
	default:
		return ""
	}
}
func (s *Session) transformValue(ctx context.Context, kind, value string, restore bool, carrier, path string) (string, error) {
	if value == "" {
		return value, nil
	}
	if kind == "cache" && !s.target.Binding.RemapCacheKeys {
		return value, nil
	}
	var derived string
	var err error
	switch {
	case strings.HasPrefix(kind, "feature:"):
		derived = value
		if !restore {
			if sample := s.profileFeature(strings.TrimPrefix(kind, "feature:")); sample != "" {
				derived = sample
			}
		}
	case kind == "serialized":
		var output []byte
		output, err = s.json(ctx, []byte(value), restore, carrier, path, true)
		derived = string(output)
	case kind == "telemetry":
		derived = value
		if restore {
			for original, mapped := range s.target.Binding.TelemetryPathMappings {
				if value == mapped {
					derived = original
					break
				}
			}
		} else if mapped, ok := s.target.Binding.TelemetryPathMappings[value]; ok {
			derived = mapped
		}
	default:
		derived, err = s.identity(ctx, kind, value, restore)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var failure *Failure
		if errors.As(err, &failure) {
			return "", failure
		}
		return "", s.failure("mapping", carrier, path, value, derived, err)
	}
	s.difference(carrier, path, value, derived)
	return derived, nil
}
