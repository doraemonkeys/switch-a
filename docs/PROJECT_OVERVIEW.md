# Project Overview

> **Doc Maintenance**: Keep concise, avoid redundancy, clean up outdated content promptly to reduce AI context usage.
> **Scope**: This document reflects the current codebase state only and does not describe future plans.
> **Goal**: Help AI quickly locate relevant code by module, type, and data flow.

## What is Switch-A

An **AI API Gateway** that proxies HTTP and WebSocket traffic to multiple AI providers (Claude, OpenAI, Gemini, etc.) with intelligent routing, resilient failover, and observability.

**Core Features:**
- **Multi-Provider Routing**: Hierarchical routing (root candidate strategy + group strategy: priority, weight, random) and model-level routing policies.
- **First-Class WebSocket Support**: Full-duplex proxy for OpenAI Realtime / Codex WS with deferred downstream handshake, client model probing, and pre-visible replay buffering.
- **Resilient Failover & Two-Phase Switching**: Pre-visible replacement (silent transparent retry across providers) vs post-visible failover (strictly scoped by vendor isolation).
- **Decoupled Credential Sessions**: Independent API Key & ChatGPT OAuth session pool with 401 auto-refresh, `ChatGPT-Account-Id` hygiene/injection, and quota window tracking.
- **Pre-Commit Probing & Error Rules**: Semantic error matching on HTTP/SSE streams before flushing to client, triggering automatic failover.
- **Concurrency & Continuity**: Lease-based concurrency limiting (with generation tags), configurable sticky affinity, and explicit provider/state continuity.
- **Observability & Diagnostics**: Token usage analytics, structured attempt evidence, real-time live monitoring, and in-memory debug traffic capture.
- **Codex Client Profiles**: Provider-scoped client disguise with persistent login devices, downstream Key bindings, platform filtering, versioned reference profiles, and field-level diagnostics.
- **Admin UI**: Embedded management dashboard for credentials, providers, client profiles, routing, error detection, logs, and portable configuration backups.

## Tech Stack

| Layer    | Stack                                                              |
|----------|--------------------------------------------------------------------|
| Backend  | Go 1.25, pure-Go SQLite (`glebarez/sqlite`, GORM), Zap, Viper, `coder/websocket` |
| Frontend | React 19, TypeScript 5.9, Vite 7, Tailwind 4, React Router 8, Lucide React |
| Testing  | Go test (CI: ≥90% total, ≥70% package), Vitest (≥40% baseline gate, ~83% logic coverage) |

## Architecture

```
┌────────────────────────────────────────────────────────┐
│          HTTP & WebSocket Gateway (port 28080)         │
└───────────┬────────────────────────────────┬───────────┘
            │ HTTP                           │ WS Upgrade
            ▼                                ▼
┌───────────────────────┐        ┌───────────────────────┐
│ internal/proxy/       │        │ internal/             │
│ (routing, SSE, probe) │        │   websocketproxy/     │
└───────────┬───────────┘        └───────────┬───────────┘
            │                                │
            └───────────────┬────────────────┘
                            ▼
┌────────────────────────────────────────────────────────┐
│                     Core Subsystems                    │
│  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐  │
│  │   selector   │ │ apicontract  │ │ responseanalysis│ │
│  └──────────────┘ └──────────────┘ └────────────────┘  │
│  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐  │
│  │codex/auth    │ │  errorrule   │ │upstreamtarget/ │  │
│  │              │ │              │ │   transport    │  │
│  └──────────────┘ └──────────────┘ └────────────────┘  │
│  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐  │
│  │requestcapture│ │    health    │ │     store      │  │
│  └──────────────┘ └──────────────┘ └────────────────┘  │
└───────────────────────────┬────────────────────────────┘
                            │
                            ▼
┌────────────────────────────────────────────────────────┐
│                 SQLite (GORM, pure Go)                 │
└────────────────────────────────────────────────────────┘
```

### Core Concepts & Runtime Policies

> **Maintenance**: Update this section alongside code changes to these concepts, defaults, or lifecycle boundaries.

- **Sticky** (`internal/selector/`): Soft Provider preference within routing, health, and concurrency constraints. `sticky_mode`: `off` / `api_type` / `model` (default); `sticky_ttl`: 300 seconds by default. Codex keys include the persistent client's canonical scope; explicit replacement-Key binding retains that scope.
- **Provider continuity** (`internal/model/switch.go`, `internal/model/continuity_seed.go`): Tracks client-visible origin and failover isolation. Request-local context is separate from one-shot, 5-second cross-request recovery seeds.
- **Codex state ownership** (`internal/codex/continuity/`): Binds conversation/state evidence to client and upstream protocol scope; Provider ID is only a route hint. Ownership is independent of Sticky.
- **Conversation recovery** (`internal/model/conversation_recovery_policy.go`, `internal/codex/http/`, `internal/codex/websocket/`): `conversation_recovery_policy` defaults to `preserve_conversation` (honor verified owner). `switch_account_preserve_conversation` permits eligible account switching while preserving original client state and its source ownership. Pre-visible attempts may be replaced; visible WebSocket failures requiring recovery use client reconnect. Routing and failover constraints still apply.
- **Provider / CredentialSession / Authority** (`internal/codex/credentialsession/`, `internal/codex/identity/`): Provider defines a route target, CredentialSession owns credentials and authentication lifecycle, and Authority identifies the upstream ownership boundary. Switching Providers need not change accounts.
- **Downstream client identity** (`internal/codex/clientidentity/`): Resolves entry API Keys to a persistent client and legacy scope aliases shared by disguise mappings, continuity and sticky routing. Explicit Key binding preserves identity; conflicting established ownership is rejected.
- **Login device & client profile** (`internal/codex/clientdisguise/`): Each credential session has an independent device/generation; shared Providers reuse it. Same-account refresh/reauthentication preserves it, account changes archive it. Tuple-specific immutable revisions follow a designated reference's version/capture watermark or a pinned revision; unspecified sampled fields remain unchanged. Transport samples apply only supported, explicitly recorded settings.
- **Disguise operation** (`internal/codex/disguiseruntime/`, `internal/codex/clientdisguise/wire/`): Provider policy/profile snapshots freeze per HTTP request or downstream WS connection. Selection evaluates original platform facts; only the final send target commits a binding. Conversion derives each transmission from original input and restores known response fields. Conversion faults terminate the operation with diagnostics and no upstream health penalty or retry bypass.
- **Portable Codex restore** (`internal/store/*codex_transfer*`, `internal/codex/keyring/`): Config `codex_state` merges devices/history, profiles/reference tracks, mappings, client aliases, continuity, HMAC material and sticky bindings atomically. Selected imports follow referenced state; settings-only preserves it. ChatGPT restores as pending reauthentication; same-account verification reuses its device. Conflicts roll back and caches publish only committed state.
- **Disclosure / ClientVisible** (`internal/upstreamtransport/`, `internal/codex/websocket/boundaries.go`): Possible upstream request disclosure and client-visible output are separate lifecycle boundaries governing recovery. An ordinary WebSocket `101` does not establish business visibility.
- **Attempts & retries** (`internal/errorrule/ledger.go`, `internal/model/switch.go`, `internal/upstreamtransport/`): Logical attempts, same-provider retries, cross-provider switches, and transport transmissions have distinct accounting; network send counts do not directly equal retry-budget consumption.
- **Usage-limit policy & health** (`internal/model/provider_usage_limit_policy.go`, `internal/errorrule/health.go`): Provider-scoped `usage_limit_policy` defaults to `switch_provider`; `suspend` opts into temporary suspension. Routing-away decisions and health verdicts are separate.

## Directory Structure

```
switch-a/
├── cmd/switch-a/              # Entry point and runtime startup composition
├── internal/                  # Go backend packages
│   ├── admin/                 # Admin HTTP API handlers
│   ├── analyticswindow/       # Time window and granularity parsing
│   ├── apicontract/           # API contract catalog, dialects, path rewrites
│   ├── attemptevidence/       # Structured attempt diagnostic evidence
│   ├── buildinfo/             # Version and build metadata
│   ├── codex/                 # Codex state, headers policy, credentials, ws protocol
│   │   ├── clientidentity/    # Persistent downstream clients and Key aliases
│   │   ├── clientdisguise/    # Login devices, profiles, learning and wire conversion
│   │   ├── disguiseruntime/   # Frozen operation proposals and final-target commits
│   │   └── credentialsession/ # Credential session store & lifecycle
│   ├── config/                # YAML/env config loading
│   ├── defaults/              # Global default constants
│   ├── errorrule/             # Semantic error matching rules & retry ledger
│   ├── health/                # Circuit breaker & availability management
│   ├── instant/               # Monotonic time abstraction
│   ├── logger/                # Zap structured logger setup
│   ├── model/                 # Domain models (Provider, Group, Log, Attempt, etc.)
│   ├── providerauth/          # ChatGPT OAuth login, token refresh, quota tracking
│   ├── proxy/                 # HTTP proxy handler, contract resolution, SSE forwarding
│   ├── requestcapture/        # Bounded logical ingress/attempt capture & HAR/JSON export
│   ├── requestingress/        # Growing wire spool, replay readers, source state & cleanup
│   │   ├── clientconnection/  # Disconnect observation independent of upload interruption
│   │   ├── h2ingress/         # TLS HTTP/2 boundary preserving undeclared trailers
│   │   └── semantic/          # Streaming decoded model, reasoning and Codex evidence
│   ├── responseanalysis/      # Multi-protocol streaming response & token analyzer
│   ├── selector/              # Selection engine (root strategy, leases, sticky cache)
│   ├── server/                # HTTP/WS server setup (proxy + admin)
│   ├── store/                 # SQLite persistence (GORM) + CachedStore wrapper
│   ├── tokenanalytics/        # Token analytics aggregation & time series
│   ├── upstreamtarget/        # Lossless provider URL construction
│   ├── upstreamtransport/     # Connection pool, per-transmission body/framing, redirects & native retries
│   ├── websocketproxy/        # WebSocket gateway, session orchestrator, replay relay
│   ├── errors.go              # Standard domain error definitions
│   └── interfaces.go          # Core interfaces (Store, HealthManager, StickyCache, Clock)
├── web/                       # React frontend (embedded in Go binary, base: /admin/)
│   └── src/
│       ├── api/               # API client, contracts, decoders, DI context
│       ├── components/        # Reusable UI components, modals, filters
│       ├── features/          # Feature domains (debug-capture, error-detection, etc.)
│       ├── hooks/             # Custom React hooks
│       ├── lib/               # Utility helpers (providerAuth, providerImport, uuid)
│       ├── pages/             # Route pages (Dashboard, Providers, Credentials, etc.)
│       └── config/            # Frontend constants
└── docs/                      # Documentation
```

## Core Domain Models

Located primarily in `internal/model/` and `internal/codex/credentialsession/`:

| Model | Location | Purpose |
|---|---|---|
| `Provider` | `internal/model/` | AI provider definition (bound credentials, concurrency, failover scopes, weight/priority) |
| `Session` | `internal/codex/credentialsession/` | Decoupled credential session (API Key or ChatGPT OAuth, token state, usage quotas) |
| `Client` / `Resolution` | `internal/codex/clientidentity/` | Persistent downstream identity, canonical scope and lookup aliases |
| `LoginIdentity` / `ProfileBinding` | `internal/codex/clientdisguise/` | Credential-owned device generation and selected tuple/revision/mode |
| `ProfileRevision` / `ProfileTrack` | `internal/codex/clientdisguise/` | Immutable feature observation and monotonic reference learning head |
| `Group` | `internal/model/` | Provider grouping with strategy (priority, weight, random) |
| `RoutingPolicy` | `internal/model/` | Model and vendor-based routing rules mapped to target providers or groups |
| `HealthState` | `internal/model/` | Circuit breaker state (available, fail count, disabled_until) |
| `RequestLog` | `internal/model/` | Canonical request log (latency, tokens, reasoning tokens, completion state, WebSocket flags) |
| `RequestAttempt` | `internal/model/` | Per-attempt diagnostic evidence (phase, health verdict, switch mode, latency) |
| `VisibleContinuitySeed` | `internal/model/` | Cross-request session continuity binding seed |
| `RuntimeConfig` | `internal/model/` | Key-value runtime configuration |

## Key Interfaces

Defined in `internal/interfaces.go` (Note: following Go best practice, `Selector` is defined consumer-side in `internal/proxy/handler.go` and `internal/websocketproxy/gateway.go`):

| Interface | Defined In | Purpose |
|---|---|---|
| `Store` | `internal/interfaces.go` | Unified persistence (providers, credential sessions, groups, health, logs, attempts) |
| `HealthManager` | `internal/interfaces.go` | Circuit breaker status, auto-disable, and cooldown recovery |
| `StickyCache` | `internal/interfaces.go` | Session affinity cache with proactive provider eviction |
| `Clock` | `internal/interfaces.go` | Monotonic and wall time abstraction for testing |
| `HTTPDoer` | `internal/interfaces.go` | HTTP request client abstraction |
| `Selector` *(consumer)* | `internal/proxy/`, `internal/websocketproxy/` | Provider selection and concurrency lease acquisition |
| `BodySource` *(consumer)* | `internal/upstreamtransport/` | Reopenable wire input with frozen framing and completed trailers |

## Request Flow

### 1. HTTP Request Flow

1. **Receive & resolve contract** → `internal/server/`, `internal/proxy/`, `internal/apicontract/`; `requestingress/clientconnection` preserves disconnect observation after deliberate HTTP/1 upload interruption, and `requestingress/h2ingress` preserves TLS HTTP/2 trailers.
2. **Start logical ingress & capture** → `internal/requestingress/`, `internal/requestcapture/`; freeze client framing, append original bytes to a memory/disk spool, retain bounded evidence.
3. **Project facts & admit selection** → `requestingress/semantic`, `internal/codex/`, `internal/selector/`; wait only for effective routing/sticky/continuity dependencies, pin the routing catalog, and publish observation-only facts when ready.
4. **Select provider & acquire lease** → `internal/selector/`; provider health and concurrency remain live within the admitted catalog.
5. **Build & forward each transmission** → `internal/upstreamtarget/`, `internal/providerauth/`, `internal/upstreamtransport/`; reopen a reader from offset zero, follow the growing input, and pair it with independent framing/trailers for retries and redirects.
6. **Classify HTTP/SSE before commit** → `internal/responseanalysis/`, `internal/errorrule/`; eligible refresh/replacement retains healthy input and waits for the old reader to close. Source failure has separate attribution from provider failure.
7. **Finish response & release ownership** → `internal/proxy/`, `internal/store/`; stop unused upload after replay closes, retain disconnect cancellation, persist evidence, release leases and clean the spool after all readers/references finish.

For enabled Codex profiles, original input remains the source for routing, ownership, error analysis and replay; physical HTTP/WS output uses the frozen target's mappings and observed features. Disguise failures carry a diagnostic ID and original/derived field evidence.

HTTP monitoring reports `upstream_body_read_bytes`: body bytes consumed across transmissions, including rereads, without implying delivery or disclosure. Ingress received bytes, final trailers and later replay-storage failure remain separate capture facts; `bytes_sent` retains WebSocket payload semantics.

### 2. WebSocket Request Flow

1. **Upgrade detected** → branched from `internal/proxy/handler.go` to `internal/websocketproxy/gateway.go`
2. **Client model probing** (independent duration, decoded-byte and work budgets; queue frames for first delivery) → `internal/websocketproxy/`, `internal/codex/websocketprotocol/`
3. **Select provider & acquire lease** → `internal/selector/`
4. **Dial upstream & 401 auto-refresh** (connect to upstream provider; retry on auth failure) → `internal/websocketproxy/`
5. **Deferred downstream handshake** (send 101 Switching Protocols only after upstream agrees) → `internal/websocketproxy/`
6. **Dual relay & pre-visible replay** (budget immutable payload and snapshot descriptor retention; exhaustion preserves queued first delivery and live forwarding) → `internal/websocketproxy/`
7. **Pre-visible replacement** (if upstream fails before downstream visibility, suppress error, select new provider, replay buffered frames)
8. **Client-visible streaming & close** (maintain session continuity, release lease, record attempt & session logs)

## Frontend Pages

Mounted under `/admin/` (React Router `basename="/admin"`):

| Page | Path (Relative / Full) | Purpose |
|---|---|---|
| Dashboard | `/` (`/admin/`) | Overview metrics, system status, provider quick actions |
| Monitor | `/monitor` (`/admin/monitor`) | Real-time request streams, latency, and active connections |
| Providers | `/providers` (`/admin/providers`) | Provider management, health status, import/export, endpoint mappings |
| Credentials | `/credentials` (`/admin/credentials`) | Credential sessions (ChatGPT OAuth & API Key), token state, quota windows |
| Client Disguise | `/client-disguise` (`/admin/client-disguise`) | Login devices, profile versions/reference sources, pinned/automatic mode, transport samples and replacement-Key bindings |
| Groups | `/groups` (`/admin/groups`) | Provider grouping and failover strategy config |
| Routing | `/routing` (`/admin/routing`) | Model- and vendor-based intelligent routing policies |
| Error Detection | `/error-detection` (`/admin/error-detection`) | Upstream semantic error matching rules and retry behavior |
| Config | `/config` (`/admin/config`) | Global runtime settings (sticky TTL, circuit breaker thresholds), backup/restore |
| Logs | `/logs` (`/admin/logs`) | Request log explorer, attempt diagnostics drill-down, deep link to error rules |
| Token Usage | `/token-usage` (`/admin/token-usage`) | Aggregated token usage analytics and breakdown by model/provider |
| Debug Capture | `/debug-capture` (`/admin/debug-capture`) | In-memory traffic trace capture sessions and HAR/JSON export |
| Login | `/login` (`/admin/login`) | Admin token authentication |
