package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const (
	// Pending owners outlive uncertain network writes long enough to prevent a
	// rapid cross-authority reclaim after a client-visible outcome is ambiguous.
	defaultContinuityPendingTTL = 24 * time.Hour
	// Committed state follows the same operational inactivity horizon as the
	// provider-Cookie handle rather than becoming an unbounded identity ledger.
	defaultContinuityCommittedIdleTTL = 30 * 24 * time.Hour
	defaultContinuityTombstoneTTL     = 7 * 24 * time.Hour
	defaultContinuityMaxPerKind       = int64(10_000)
)

type applicationCodexRuntime struct {
	identities      *clientidentity.Resolver
	HTTP            *codexhttp.Runtime
	WebSocket       *codexws.Runtime
	continuity      *codexcontinuity.Service
	providerCookies *providercookie.Service
	maintenance     *applicationCodexMaintenance
}

func newApplicationCodexRuntime(
	ctx context.Context,
	persistence *store.SQLiteStore,
	security *applicationCodexSecurity,
	clock internal.Clock,
	log *zap.Logger,
) (*applicationCodexRuntime, error) {
	if persistence == nil || security == nil || security.keyring == nil || clock == nil || log == nil {
		return nil, fmt.Errorf("initialize Codex runtime: persistence, keyring, clock, and logger are required")
	}
	scheme := codexhttp.NewTrustedProxySchemeResolver(nil)
	digester, continuity, cookies, err := newApplicationCodexServices(
		ctx,
		persistence,
		security,
		rand.Reader,
		providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		clock,
		log,
	)
	if err != nil {
		return nil, err
	}

	identities, err := persistence.ClientIdentityResolver(digester)
	if err != nil {
		return nil, err
	}
	identities.SetObserver(func(event clientidentity.Trace) {
		fields := []zap.Field{zap.String("client_identity_id", event.ClientID), zap.String("decision", event.Decision), zap.Int("alias_count", event.AliasCount)}
		if event.Err != nil {
			log.Error("codex.client_identity", append(fields, zap.Error(event.Err))...)
		} else {
			log.Debug("codex.client_identity", fields...)
		}
	})
	httpRuntime, err := codexhttp.New(codexhttp.Config{
		ClientIdentities: identities, Continuity: continuity,
		ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Codex HTTP runtime: %w", err)
	}
	webSocketRuntime, err := codexws.New(codexws.Config{
		ClientIdentities: identities, Continuity: continuity,
		ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Codex WebSocket runtime: %w", err)
	}
	return &applicationCodexRuntime{
		HTTP: httpRuntime, WebSocket: webSocketRuntime, identities: identities,
		continuity:      continuity,
		providerCookies: cookies,
	}, nil
}

func newApplicationCodexServices(
	ctx context.Context,
	persistence *store.SQLiteStore,
	security *applicationCodexSecurity,
	random io.Reader,
	hostCanonicalizer providercookie.HostCanonicalizer,
	clock internal.Clock,
	log *zap.Logger,
) (*codexidentity.Digester, *codexcontinuity.Service, *providercookie.Service, error) {
	if persistence == nil || security == nil || security.keyring == nil || random == nil || hostCanonicalizer == nil || clock == nil || log == nil {
		return nil, nil, nil, fmt.Errorf("initialize Codex services: persistence, keyring, random source, host canonicalizer, clock, and logger are required")
	}
	identityDigester, err := codexidentity.NewDigester(security.keyring)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize Codex identity digester: %w", err)
	}
	digester := &identityDigester
	repositories, err := persistence.OpenCodexRepositories(ctx, security.keyring)
	if err != nil {
		return nil, nil, nil, err
	}
	policy, err := defaultContinuityPolicy()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize Codex continuity policy: %w", err)
	}
	continuity, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store:    repositories.Continuity,
		Digester: digester,
		Policy:   policy,
		Clock:    clock,
		Observer: continuityLogObserver(log),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize Codex continuity service: %w", err)
	}
	cookies, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository:        repositories.ProviderCookies,
		HandleDigester:    security.keyring,
		Random:            random,
		Clock:             clock,
		HostCanonicalizer: hostCanonicalizer,
		PublicSuffixList:  codexidentity.PublicSuffixList{},
		Policy:            providercookie.DefaultPolicy(),
		Trace:             providerCookieLogTrace(log),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize provider-Cookie service: %w", err)
	}
	return digester, continuity, cookies, nil
}

func defaultContinuityPolicy() (codexcontinuity.Policy, error) {
	limits := codexcontinuity.Limits{
		PendingTTL: defaultContinuityPendingTTL, CommittedIdleTTL: defaultContinuityCommittedIdleTTL,
		TombstoneTTL: defaultContinuityTombstoneTTL, MaxBindings: defaultContinuityMaxPerKind,
	}
	return codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID:          limits,
		codexcontinuity.KindSessionID:         limits,
		codexcontinuity.KindConversationID:    limits,
		codexcontinuity.KindWindowID:          limits,
		codexcontinuity.KindTurnState:         limits,
		codexcontinuity.KindTurnMetadata:      limits,
		codexcontinuity.KindResponseReference: limits,
	})
}

func continuityLogObserver(log *zap.Logger) codexcontinuity.Observer {
	return codexcontinuity.ObserverFunc(func(event codexcontinuity.Event) {
		log.Debug("codex.continuity_decision",
			zap.Time("at", event.At),
			zap.String("action", event.Action),
			zap.String("outcome", event.Outcome),
			zap.String("operation_id", event.OperationID),
			zap.String("session_id", event.SessionID),
			zap.String("connection_generation", string(event.Generation)),
			zap.String("binding_kind", string(event.BindingKind)),
			zap.String("lifecycle", string(event.Lifecycle)),
			zap.String("key_version", event.KeyVersion),
			zap.String("client_key_version", event.ClientVersion),
			zap.String("protocol_scope", event.ProtocolScope),
			zap.String("route_target_hint", event.RouteTargetHint),
		)
	})
}

func providerCookieLogTrace(log *zap.Logger) providercookie.TraceSink {
	return providercookie.TraceSinkFunc(func(event providercookie.TraceEvent) {
		log.Debug("codex.provider_cookie_decision",
			zap.String("operation_id", string(event.OperationID)),
			zap.String("milestone", event.Milestone),
			zap.String("decision", event.Decision),
			zap.String("reason", event.Reason),
			zap.Int("count", event.Count),
			zap.Int("rejected", event.Rejected),
			zap.Int("evicted", event.Evicted),
		)
	})
}
