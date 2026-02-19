# Project Overview

> **Doc Maintenance**: Keep concise, avoid redundancy, clean up outdated content promptly to reduce AI context usage.
> **Scope**: This document reflects the current codebase state only and does not describe future plans.
> **Goal**: Help AI quickly locate relevant code by module, type, and data flow.

## What is Switch-AAn **AI API Gateway** that proxies requests to multiple AI providers (Claude, OpenAI, Gemini, etc.) with intelligent routing, failover, and observability.

**Core Features:**
- Multi-provider routing with selection strategies (priority, weight, random)
- Circuit breaker + health checks for automatic failover
- Vendor isolation for failover scope control
- Sticky sessions (session affinity by IP/user)
- Per-provider concurrency limiting
- Request logging with token usage tracking
- Admin UI for configuration and monitoring

## Tech Stack

| Layer    | Stack                                        |
|----------|----------------------------------------------|
| Backend  | Go 1.25, SQLite (GORM), Zap logger, Viper    |
| Frontend | React 19, TypeScript 5.9, Vite 7, Tailwind 4 |
| Testing  | Go test (90% coverage), Vitest (40% coverage)|## Architecture

```
┌─────────────────┐     ┌─────────────────┐
│  Proxy Server   │     │  Admin Server   │
│   (port 28080)  │     │   (port 28081)  │
└────────┬────────┘     └────────┬────────┘
         │                       │
         ▼                       ▼
┌─────────────────────────────────────────┐
│              internal/                   │
│  ┌─────────┐ ┌──────────┐ ┌──────────┐  │
│  │  proxy  │ │ selector │ │  health  │  │
│  └─────────┘ └──────────┘ └──────────┘  │
│  ┌─────────┐ ┌──────────┐ ┌──────────┐  │
│  │  store  │ │  model   │ │  config  │  │
│  └─────────┘ └──────────┘ └──────────┘  │
└─────────────────────────────────────────┘
         │
         ▼
┌─────────────────┐
│  SQLite (GORM)  │
└─────────────────┘
```## Directory Structure```
switch-a/
├── cmd/switch-a/          # Entry point (main.go)
├── internal/              # Go backend packages
│   ├── config/            # YAML/env config loading
│   ├── model/             # Domain models: Provider, Group, HealthState, RequestLog
│   ├── store/             # SQLite persistence + CachedStore wrapper
│   ├── proxy/             # HTTP proxy: routing, SSE, token capture
│   ├── selector/          # Provider selection: strategy, sticky, concurrency
│   ├── health/            # Circuit breaker, availability tracking
│   ├── server/            # HTTP server setup (proxy + admin)
│   ├── admin/             # Admin API handlers
│   └── interfaces.go      # Core interfaces (Store, Selector, HealthManager)
├── web/                   # React frontend (embedded in Go binary)
│   └── src/
│       ├── api/           # API client with DI support
│       ├── components/    # Reusable UI components
│       ├── pages/         # Route pages (Dashboard, Providers, Groups, Config, Logs, Monitor)
│       ├── hooks/         # Custom hooks (useConfig, useConfigExport)
│       └── config/        # Frontend constants
└── docs/                  # Documentation
```

## Core Domain Models

Located in `internal/model/model.go`:

| Model           | Purpose                                         |
|-----------------|-------------------------------------------------|
| `Provider`      | AI provider config (API key, weight, priority, backoff, vendor, per-API-type base URLs) |
| `Group`         | Provider grouping with strategy (priority/weight/random) |
| `HealthState`   | Circuit breaker state (available, fail counts, disabled_until) |
| `RequestLog`    | Request log with latency, tokens, retry info    |
| `RequestAttempt`| Individual attempt within a request (for retry tracking) |
| `RuntimeConfig` | Key-value runtime configuration                 |

## Key Interfaces

Defined in `internal/interfaces.go`:

| Interface       | Purpose                                         |
|-----------------|-------------------------------------------------|
| `Store`         | Data persistence (providers, groups, health, logs, config) |
| `Selector`      | Provider selection logic                        |
| `HealthManager` | Circuit breaker, availability checks            |
| `StickyCache`   | Session affinity cache                          |
| `Clock`         | Time abstraction for testing                    |

## Request Flow

1. **Proxy receives request** → `internal/proxy/handler.go`
2. **Extract API type** from path → `internal/proxy/extractor.go`
3. **Select provider** (strategy + health + sticky + concurrency) → `internal/selector/selector.go`
4. **Forward request** with retry/failover → `internal/proxy/transport.go`
5. **Log result** (tokens, latency, attempts) → `internal/store/sqlite_logs.go`

## Frontend Pages

| Page       | Path         | Purpose                              |
|------------|--------------|--------------------------------------|
| Dashboard  | `/admin/`    | Overview stats, quick actions        |
| Monitor    | `/monitor`   | Real-time request monitoring         |
| Providers  | `/providers` | Provider CRUD, health status         |
| Groups     | `/groups`    | Group management, strategy config    |
| Config     | `/config`    | Runtime config (sticky TTL, circuit breaker thresholds) |
| Logs       | `/logs`      | Request log viewer with filters      |
