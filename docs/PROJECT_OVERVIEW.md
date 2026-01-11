# Project Overview

> **Doc Maintenance**: Keep concise, avoid redundancy, clean up outdated content promptly to reduce AI context usage.
> **Scope**: This document reflects the current codebase state only and does not describe future plans.
> **Goal**: Help AI quickly locate relevant code by module, type, and data flow.

## Architecture

Switch-A is an AI provider proxy pool service with automatic failover.

```
Client → HTTP Server → Proxy Handler → Selector → Provider → Upstream AI API
                            ↓              ↓
                      Health Manager   Sticky Cache
                            ↓
                      Circuit Breaker
```

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/switch-a` | Main entry point, server setup |
| `internal/config` | Environment variable loading |
| `internal/model` | Data models (Provider, Group, HealthState, etc.) |
| `internal/store` | SQLite storage implementation |
| `internal/proxy` | HTTP proxy handler, routing, headers, transport |
| `internal/selector` | Provider selection strategies, sticky cache, concurrency |
| `internal/health` | Circuit breaker, health management |
| `internal/server` | HTTP server setup, route registration |
| `internal/admin` | Management API handlers, authentication middleware |
| `internal/logger` | Zap logger initialization |

## Key Interfaces

```go
// internal/interfaces.go
type Store interface { ... }         // Data persistence
type Selector interface { ... }      // Provider selection
type HealthManager interface { ... } // Health tracking & circuit breaking
type StickyCache interface { ... }   // Session affinity cache
```

## Selection Flow

1. **Sticky Cache Check**: Return cached provider if valid
2. **Health Filter**: Exclude unhealthy/circuit-broken providers
3. **Group Selection**: Pick group using inter-group strategy (priority/weight/random)
4. **Provider Selection**: Pick provider using group's strategy
5. **Concurrency Check**: Skip providers at concurrency limit
6. **Retry on Failure**: Exclude failed providers, try next

## Strategies

- `priority`: Lower priority value = higher precedence
- `weight`: Weighted random selection
- `random`: Pure random selection

## Circuit Breaker

- Sliding window tracks failures per provider
- Threshold failures → auto-disable for configured duration
- Auto-recover after disable period expires
- Manual enable/disable supported

## Management API

All admin endpoints require `Authorization: Bearer <admin_token>` header.

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/admin/api/providers` | GET/POST | List/Create providers |
| `/admin/api/providers/{id}` | GET/PUT/DELETE | Get/Update/Delete provider |
| `/admin/api/providers/{id}/enable` | POST | Enable provider |
| `/admin/api/providers/{id}/disable` | POST | Disable provider |
| `/admin/api/providers/{id}/reset` | POST | Reset circuit breaker |
| `/admin/api/groups` | GET/POST | List/Create groups |
| `/admin/api/groups/{id}` | GET/PUT/DELETE | Get/Update/Delete group |
| `/admin/api/config` | GET/PUT | Get/Update runtime config |
| `/admin/api/health` | GET | Health states of all providers |
| `/admin/api/status` | GET | System status with concurrency info |
| `/admin/api/logs` | GET | Request logs (paginated) |