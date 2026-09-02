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

type Clock interface {
	Now() time.Time
}

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
		repository: config.Repository,
		digester:   config.HandleDigester,
		random:     config.Random,
		clock:      config.Clock,
		parser:     parser,
		hosts:      config.HostCanonicalizer,
		policy:     config.Policy,
		trace:      config.Trace,
	}, nil
}

type JarAccess struct {
	jarID       JarID
	handleValue string
	issued      bool
	refresh     bool
}

func (a JarAccess) JarID() JarID        { return a.jarID }
func (a JarAccess) HandleValue() string { return a.handleValue }
func (a JarAccess) Issued() bool        { return a.issued }
func (a JarAccess) Refresh() bool       { return a.refresh }

func (a JarAccess) String() string   { return "provider-cookie-jar-access(handle=redacted,jar=redacted)" }
func (a JarAccess) GoString() string { return a.String() }

func (s *Service) ResolveJar(
	ctx context.Context,
	operationID OperationID,
	rawHandle string,
	clientScopes []codexidentity.ClientScope,
) (JarAccess, error) {
	if ctx == nil {
		return JarAccess{}, &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if _, err := NewOperationID(string(operationID)); err != nil {
		return JarAccess{}, err
	}
	if err := validateClientScopes(clientScopes); err != nil {
		return JarAccess{}, err
	}

	// Only a handle returned by the client authorizes reuse. Falling back to a
	// client credential would silently add provider state that the client chose
	// not to retain.
	if rawHandle == "" {
		return s.issueEmptyJar(ctx, operationID, clientScopes[0], "missing")
	}
	if !canonicalHandleValue(rawHandle) {
		return s.issueEmptyJar(ctx, operationID, clientScopes[0], "malformed")
	}
	digests, err := s.digester.LookupDigests(codexkeyring.HMACJarHandle, []byte(rawHandle))
	if err != nil {
		return JarAccess{}, s.persistenceFailure(operationID, "resolve_handle", PersistenceCrypto, err)
	}
	use, err := s.repository.UseBinding(ctx, BindingLookup{
		HandleDigests: digests,
		ClientScopes:  append([]codexidentity.ClientScope(nil), clientScopes...),
		At:            canonicalTime(s.clock.Now()),
		Policy:        s.policy,
	})
	if err != nil {
		return JarAccess{}, s.persistenceFailure(operationID, "resolve_handle", PersistenceUnavailable, err)
	}
	if use.Disposition != BindingValid {
		return s.issueEmptyJar(ctx, operationID, clientScopes[0], string(use.Disposition))
	}
	s.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: operationID,
		Milestone:   "handle_resolved",
		Decision:    "reuse",
		Reason:      "valid",
	})
	return JarAccess{jarID: use.Record.JarID, handleValue: rawHandle, refresh: use.Refresh}, nil
}

func (s *Service) issueEmptyJar(
	ctx context.Context,
	operationID OperationID,
	clientScope codexidentity.ClientScope,
	reason string,
) (JarAccess, error) {
	for attempt := 0; attempt < bindingGenerationAttempts; attempt++ {
		handleBytes := make([]byte, GatewayHandleEntropyBytes)
		if _, err := io.ReadFull(s.random, handleBytes); err != nil {
			return JarAccess{}, s.persistenceFailure(operationID, "generate_handle", PersistenceCrypto, err)
		}
		handleValue := base64.RawURLEncoding.EncodeToString(handleBytes)
		clear(handleBytes)
		digest, err := s.digester.Sign(codexkeyring.HMACJarHandle, []byte(handleValue))
		if err != nil {
			return JarAccess{}, s.persistenceFailure(operationID, "sign_handle", PersistenceCrypto, err)
		}

		jarBytes := make([]byte, JarIDEntropyBytes)
		if _, err := io.ReadFull(s.random, jarBytes); err != nil {
			return JarAccess{}, s.persistenceFailure(operationID, "generate_jar", PersistenceCrypto, err)
		}
		jarID, err := JarIDFromBytes(jarBytes)
		clear(jarBytes)
		if err != nil {
			continue
		}
		now := canonicalTime(s.clock.Now())
		record := BindingRecord{
			HandleDigest:      digest,
			JarID:             jarID,
			ClientScope:       clientScope,
			CreatedAt:         now,
			LastAccessAt:      now,
			IdleExpiresAt:     addDurationClamped(now, s.policy.HandleIdleTTL),
			AbsoluteExpiresAt: addDurationClamped(now, s.policy.HandleAbsoluteTTL),
		}
		err = s.repository.CreateBinding(ctx, record, s.policy)
		if err != nil {
			if errors.Is(err, ErrIdentifierClash) {
				continue
			}
			return JarAccess{}, s.persistenceFailure(operationID, "create_binding", PersistenceUnavailable, err)
		}
		s.trace.RecordProviderCookieTrace(TraceEvent{
			OperationID: operationID,
			Milestone:   "handle_resolved",
			Decision:    "issue_empty_jar",
			Reason:      reason,
		})
		return JarAccess{jarID: jarID, handleValue: handleValue, issued: true}, nil
	}
	return JarAccess{}, s.persistenceFailure(operationID, "create_binding", PersistenceUnavailable, ErrIdentifierClash)
}

func (s *Service) Cleanup(
	ctx context.Context,
	operationID OperationID,
	reachable []codexidentity.CookieAuthority,
) (CleanupResult, error) {
	if ctx == nil {
		return CleanupResult{}, &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	if _, err := NewOperationID(string(operationID)); err != nil {
		return CleanupResult{}, err
	}
	result, err := s.repository.Cleanup(ctx, CleanupRequest{
		At:                   canonicalTime(s.clock.Now()),
		Policy:               s.policy,
		ReachableAuthorities: append([]codexidentity.CookieAuthority(nil), reachable...),
	})
	if err != nil {
		return CleanupResult{}, s.persistenceFailure(operationID, "cleanup", PersistenceUnavailable, err)
	}
	s.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: operationID,
		Milestone:   "cleanup_completed",
		Decision:    "committed",
		Count:       result.ExpiredBindings + result.ExpiredCookies + result.OrphanAuthorities + result.EmptyAuthorities,
	})
	return result, nil
}

func (s *Service) persistenceFailure(
	operationID OperationID,
	operation string,
	kind PersistenceErrorKind,
	cause error,
) error {
	var typed *PersistenceError
	if errors.As(cause, &typed) {
		kind = typed.Kind
	}
	s.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: operationID,
		Milestone:   operation,
		Decision:    "failed_closed",
		Reason:      string(kind),
	})
	if errors.As(cause, &typed) {
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
	if err != nil || len(decoded) != GatewayHandleEntropyBytes {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(decoded) == value
}
