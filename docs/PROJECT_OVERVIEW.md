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
- **Concurrency & Continuity**: Lease-based concurrency limiting (with generation tags) and sticky session affinity (IP, user, or model).
- **Observability & Diagnostics**: Token usage analytics, structured attempt evidence, real-time live monitoring, and in-memory debug traffic capture.
- **Admin UI**: Embedded management dashboard for credentials, providers, routing, error detection, logs, and config.

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
│   ├── requestcapture/        # In-memory debug traffic capture & HAR/JSON export
│   ├── responseanalysis/      # Multi-protocol streaming response & token analyzer
│   ├── selector/              # Selection engine (root strategy, leases, sticky cache)
│   ├── server/                # HTTP/WS server setup (proxy + admin)
│   ├── store/                 # SQLite persistence (GORM) + CachedStore wrapper
│   ├── tokenanalytics/        # Token analytics aggregation & time series
│   ├── upstreamtarget/        # Lossless provider URL construction
│   ├── upstreamtransport/     # Low-level HTTP transport connection pool
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

## Request Flow

### 1. HTTP Request Flow
1. **Receive & parse request** → `internal/proxy/handler.go`
2. **Resolve API contract** (method, path catalog, dialect) → `internal/apicontract/`
3. **Begin context & capture** (Codex state, thread ID, trace capture) → `internal/codex/`, `internal/requestcapture/`
4. **Select provider & acquire lease** (root candidate strategy + sticky + concurrency) → `internal/selector/`
5. **Build target & inject credentials** (lossless URL build, OAuth header injection) → `internal/upstreamtarget/`, `internal/providerauth/`
6. **Forward request** (HTTP/SSE transport; 401 triggers in-place token refresh & retry) → `internal/upstreamtransport/`
7. **Pre-commit probing & error rule matching** (inspect response before client sees bytes; trigger silent replacement on failure) → `internal/responseanalysis/`, `internal/errorrule/`
8. **Log result & attempt evidence** (tokens, latency, continuity seed, release lease) → `internal/store/`

### 2. WebSocket Request Flow
1. **Upgrade detected** → branched from `internal/proxy/handler.go` to `internal/websocketproxy/gateway.go`
2. **Client model probing** (probe initial frame budget if model unspecified) → `internal/codex/websocketprotocol/`
3. **Select provider & acquire lease** → `internal/selector/`
4. **Dial upstream & 401 auto-refresh** (connect to upstream provider; retry on auth failure) → `internal/websocketproxy/`
5. **Deferred downstream handshake** (send 101 Switching Protocols only after upstream agrees) → `internal/websocketproxy/`
6. **Dual relay & pre-visible buffer** (buffer client upstream frames before first downstream byte) → `internal/websocketproxy/`
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
| Groups | `/groups` (`/admin/groups`) | Provider grouping and failover strategy config |
| Routing | `/routing` (`/admin/routing`) | Model- and vendor-based intelligent routing policies |
| Error Detection | `/error-detection` (`/admin/error-detection`) | Upstream semantic error matching rules and retry behavior |
| Config | `/config` (`/admin/config`) | Global runtime settings (sticky TTL, circuit breaker thresholds), backup/restore |
| Logs | `/logs` (`/admin/logs`) | Request log explorer, attempt diagnostics drill-down, deep link to error rules |
| Token Usage | `/token-usage` (`/admin/token-usage`) | Aggregated token usage analytics and breakdown by model/provider |
| Debug Capture | `/debug-capture` (`/admin/debug-capture`) | In-memory traffic trace capture sessions and HAR/JSON export |
| Login | `/login` (`/admin/login`) | Admin token authentication |
