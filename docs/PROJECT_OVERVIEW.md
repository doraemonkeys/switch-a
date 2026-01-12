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
| `internal/defaults` | Centralized default values |
| `internal/model` | Data models (Provider, Group, HealthState, etc.) |
| `internal/store` | SQLite storage with caching layer |
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
