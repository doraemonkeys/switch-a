package providercookie

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

const bindingGenerationAttempts = 4

type Clock interface{ Now() time.Time }
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type HandleDigester interface {
	Sign(codexkeyring.HMACPurpose, []byte) (codexkeyring.Digest, error)
	LookupDigests(codexkeyring.HMACPurpose, []byte) ([]codexkeyring.Digest, error)
}

type ServiceConfig struct {
	Repository        Repository
	HandleDigester    HandleDigester
	Random            io.Reader
	Clock             Clock
	HostCanonicalizer HostCanonicalizer
	PublicSuffixList  PublicSuffixList
	Policy            Policy
	Trace             TraceSink
}

type Service struct {
	repository Repository
	digester   HandleDigester
	random     io.Reader
	clock      Clock
	parser     Parser
	hosts      HostCanonicalizer
	policy     Policy
	trace      TraceSink
}

func NewService(config ServiceConfig) (*Service, error) {
	if isNilDependency(config.Repository) {
		return nil, &ConfigurationError{Field: "repository", Reason: "must be provided"}
	}
	if isNilDependency(config.HandleDigester) {
		return nil, &ConfigurationError{Field: "handle_digester", Reason: "must be provided"}
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Clock == nil {
		config.Clock = wallClock{}
	}
	if config.Trace == nil {
		config.Trace = discardTrace{}
	}
	parser, err := NewParser(config.HostCanonicalizer, config.PublicSuffixList, config.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{
		repository: config.Repository, digester: config.HandleDigester,
		random: config.Random, clock: config.Clock, parser: parser,
		hosts: config.HostCanonicalizer, policy: config.Policy, trace: config.Trace,
	}, nil
}

// BeginRequest owns the binding lease until DiscardAll, including the interval
// between committing cookies and publishing the response to the client.
func (s *Service) BeginRequest(ctx context.Context, operationID OperationID, rawHandle string, clientScopes []codexidentity.ClientScope) (*Request, error) {
	if ctx == nil {
		return nil, &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if _, err := NewOperationID(string(operationID)); err != nil {
		return nil, err
	}
	if err := validateClientScopes(clientScopes); err != nil {
		return nil, err
	}
	request := &Request{
		service: s, operationID: operationID, clientScope: clientScopes[0],
		overlays: make(map[CookieScope]*Overlay),
	}
	reason := "missing"
	// A credential identifies the owner, not permission to inherit cookies.
	// Only an explicitly returned handle authorizes persistent state reuse.
	if rawHandle != "" {
		reason = "malformed"
	}
	if canonicalHandleValue(rawHandle) {
		digests, err := s.digester.LookupDigests(codexkeyring.HMACJarHandle, []byte(rawHandle))
		if err != nil {
			return nil, s.persistenceFailure(operationID, "resolve_handle", PersistenceCrypto, err)
		}
		use, err := s.repository.UseBinding(ctx, BindingLookup{
			HandleDigests: digests, ClientScopes: append([]codexidentity.ClientScope(nil), clientScopes...),
			At: canonicalTime(s.clock.Now()), Policy: s.policy,
		})
		if err != nil {
			return nil, s.persistenceFailure(operationID, "resolve_handle", PersistenceUnavailable, err)
		}
		reason = string(use.Disposition)
		if use.Disposition == BindingValid {
			request.jarID = use.Record.JarID
			request.persisted = true
			request.handleValue = rawHandle
			request.publishHandle = use.Refresh
			request.release = use.Release
			request.trace("handle_resolved", "reuse", reason, 0, 0, 0)
			return request, nil
		}
	}
	jarID, err := s.generateJar(operationID)
	if err != nil {
		return nil, err
	}
	request.jarID = jarID
	request.trace("handle_resolved", "transient", reason, 0, 0, 0)
	return request, nil
}

func (s *Service) generateJar(operationID OperationID) (JarID, error) {
	for range bindingGenerationAttempts {
		value := make([]byte, JarIDEntropyBytes)
		if _, err := io.ReadFull(s.random, value); err != nil {
			return JarID{}, s.persistenceFailure(operationID, "generate_jar", PersistenceCrypto, err)
		}
		id, err := JarIDFromBytes(value)
		clear(value)
		if err == nil {
			return id, nil
		}
	}
	return JarID{}, s.persistenceFailure(operationID, "generate_jar", PersistenceCrypto, ErrIdentifierClash)
}

func (s *Service) newBinding(operationID OperationID, jarID JarID, owner codexidentity.ClientScope, at time.Time) (BindingRecord, string, error) {
	value := make([]byte, GatewayHandleEntropyBytes)
	if _, err := io.ReadFull(s.random, value); err != nil {
		return BindingRecord{}, "", s.persistenceFailure(operationID, "generate_handle", PersistenceCrypto, err)
	}
	handle := base64.RawURLEncoding.EncodeToString(value)
	clear(value)
	digest, err := s.digester.Sign(codexkeyring.HMACJarHandle, []byte(handle))
	if err != nil {
		return BindingRecord{}, "", s.persistenceFailure(operationID, "sign_handle", PersistenceCrypto, err)
	}
	return BindingRecord{
		HandleDigest: digest, JarID: jarID, ClientScope: owner,
		CreatedAt: at, LastAccessAt: at,
		IdleExpiresAt:     addDurationClamped(at, s.policy.HandleIdleTTL),
		AbsoluteExpiresAt: addDurationClamped(at, s.policy.HandleAbsoluteTTL),
	}, handle, nil
}

func (s *Service) Cleanup(ctx context.Context, operationID OperationID, reachable []codexidentity.CookieAuthority) (CleanupResult, error) {
	if ctx == nil {
		return CleanupResult{}, &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if _, err := NewOperationID(string(operationID)); err != nil {
		return CleanupResult{}, err
	}
	result, err := s.repository.Cleanup(ctx, CleanupRequest{
		At: canonicalTime(s.clock.Now()), Policy: s.policy,
		ReachableAuthorities: append([]codexidentity.CookieAuthority(nil), reachable...),
	})
	if err != nil {
		return CleanupResult{}, s.persistenceFailure(operationID, "cleanup", PersistenceUnavailable, err)
	}
	s.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: operationID, Milestone: "cleanup_completed", Decision: "committed",
		Count:             result.ExpiredBindings + result.EmptyBindings + result.ExpiredCookies + result.OrphanAuthorities + result.EmptyAuthorities,
		ReclaimedBindings: result.ExpiredBindings + result.EmptyBindings,
	})
	return result, nil
}

func (s *Service) persistenceFailure(operationID OperationID, operation string, kind PersistenceErrorKind, cause error) error {
	var limit *LimitError
	if errors.As(cause, &limit) {
		s.trace.RecordProviderCookieTrace(TraceEvent{
			OperationID: operationID, Milestone: operation, Decision: "capacity_rejected",
			Reason: "capacity_exhausted", Limit: limit.Limit, Actual: limit.Actual, Maximum: limit.Max,
		})
		return limit
	}
	var typed *PersistenceError
	if errors.As(cause, &typed) {
		kind = typed.Kind
	}
	s.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: operationID, Milestone: operation, Decision: "failed_closed", Reason: string(kind),
	})
	if typed != nil {
		return typed
	}
	return &PersistenceError{Kind: kind, Operation: operation, Cause: cause}
}

func validateClientScopes(scopes []codexidentity.ClientScope) error {
	if len(scopes) == 0 {
		return &ConfigurationError{Field: "client_scopes", Reason: "at least one scope is required"}
	}
	seen := make(map[codexidentity.ClientScope]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, err := scope.MarshalBinary(); err != nil {
			return &ConfigurationError{Field: "client_scopes", Reason: "contains an invalid scope"}
		}
		if _, exists := seen[scope]; exists {
			return &ConfigurationError{Field: "client_scopes", Reason: "contains a duplicate scope"}
		}
		seen[scope] = struct{}{}
	}
	return nil
}

func canonicalHandleValue(value string) bool {
	if len(value) != GatewayHandleEncodedLength {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == GatewayHandleEntropyBytes && base64.RawURLEncoding.EncodeToString(decoded) == value
}
