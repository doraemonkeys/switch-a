# Global Model Mapping Feature — Implementation Plan

## Overview

Add global model mapping (aliasing) functionality: requests containing model name A are automatically rewritten to model name B before forwarding upstream. Mappings are managed via Admin UI and stored in SQLite.

**Example**: Client sends `"model": "gpt-4o-mini"` → proxy rewrites to `"model": "gpt-5.2"` → upstream receives `gpt-5.2`.

---

## Design Decisions

### D1: Mapping granularity — global, not per-api_type

Mappings apply regardless of `api_type`. Rationale: the same model name (e.g. `gpt-4o-mini`) means the same thing across Claude/Codex/Gemini protocols. If per-api_type filtering is needed later, it can be added as an optional field without breaking the schema.

### D2: Body-level rewrite, not header-level

The `model` field lives in the JSON request body (Claude/Codex) or URL path (Gemini). Rewriting happens at the byte level before forwarding, ensuring the upstream provider receives the mapped model name.

### D3: Exact match only, no wildcards

Each mapping is a simple `from → to` pair with exact string matching. Wildcard/regex patterns add complexity without clear value for v1.

### D4: Cache the full mapping table in memory

Mappings are read on every request but written rarely. Cache the entire `map[string]string` in `CachedStore` with write-through invalidation — same pattern as `RuntimeConfig` caching.

### D5: Gemini URL path rewriting

Gemini embeds the model in the URL path (`/models/{model}:generateContent`). The rewrite must handle both JSON body replacement (Claude/Codex) and URL path replacement (Gemini).

---

## Phase 1: Backend — Data Model & Store

### 1.1 Model definition

**File**: `internal/model/model.go`

```go
type ModelMapping struct {
    ID        string    `gorm:"primaryKey"              json:"id"`
    From      string    `gorm:"uniqueIndex;not null"    json:"from"`
    To        string    `gorm:"not null"                json:"to"`
    Enabled   bool      `gorm:"not null;default:true"   json:"enabled"`
    CreatedAt time.Time `                               json:"created_at"`
    UpdatedAt time.Time `                               json:"updated_at"`
}
```

- `From` has a unique index — each source model maps to exactly one target.
- `ID` is user-supplied (slug-style, e.g. `"mini-to-5.2"`), consistent with `Provider.ID`.

### 1.2 Store interface

**File**: `internal/interfaces.go` — add to `Store` interface:

```go
ListModelMappings(ctx context.Context) ([]model.ModelMapping, error)
GetModelMapping(ctx context.Context, id string) (*model.ModelMapping, error)
CreateModelMapping(ctx context.Context, m *model.ModelMapping) error
UpdateModelMapping(ctx context.Context, m *model.ModelMapping) error
DeleteModelMapping(ctx context.Context, id string) error
```

### 1.3 SQLite implementation

**New file**: `internal/store/sqlite_model_mapping.go`

- Standard GORM CRUD, following `sqlite_provider.go` patterns.
- `CreateModelMapping`: check `From` uniqueness, return conflict error if duplicate.
- `UpdateModelMapping`: allow changing `From`, `To`, `Enabled`. Validate uniqueness on `From` change.
- Add `&model.ModelMapping{}` to `AutoMigrate` in `sqlite.go`.

### 1.4 Cache layer

**File**: `internal/store/cached_store.go`

Add a `modelMappings` cache field:

```go
type CachedStore struct {
    internal.Store
    // existing config cache...

    mappingMu    sync.RWMutex
    mappingCache map[string]string  // from → to (enabled only)
    mappingExp   time.Time
}
```

- `LookupModelMapping(ctx, from) → (to string, found bool)`: hot-path method, read-locked.
- Write operations (`Create/Update/Delete`) invalidate the cache.
- Cache TTL: 5 seconds (same as config), auto-refresh on next read.

### 1.5 Tests

**New file**: `internal/store/sqlite_model_mapping_test.go`

- CRUD lifecycle test.
- Uniqueness constraint test (`From` collision).
- Cache invalidation test in `cached_store_test.go`.

---

## Phase 2: Backend — Proxy Rewriting

### 2.1 Proxy store interface

**File**: `internal/proxy/handler.go` — add to proxy-local `Store` interface:

```go
LookupModelMapping(ctx context.Context, from string) (to string, found bool)
```

This is the only method the proxy needs — no full CRUD.

### 2.2 Rewrite logic

**File**: `internal/proxy/extractor.go` — add:

```go
// ReplaceModelInBody rewrites the "model" field in JSON body bytes.
// Returns the modified body and the new model name if replaced, or the original if no match.
func ReplaceModelInBody(body []byte, from, to string) []byte {
    // Use modelFieldRe to locate the "model":"..." span
    // Replace the captured group (model name) with `to`
    // Return modified bytes
}
```

For Gemini, add a separate function:

```go
// ReplaceModelInGeminiPath rewrites /models/{model}:action paths.
func ReplaceModelInGeminiPath(path, from, to string) string
```

### 2.3 Integration in handler

**File**: `internal/proxy/handler.go` — in `ServeHTTP`, after model extraction (around line 229):

```go
// Extract original model
originalModel := ExtractModel(r, apiType, body)

// Apply model mapping
mappedModel := originalModel
if to, found := h.store.LookupModelMapping(ctx, originalModel); found {
    mappedModel = to
    if apiType == APITypeGemini {
        r.URL.Path = ReplaceModelInGeminiPath(r.URL.Path, originalModel, to)
    } else {
        body = ReplaceModelInBody(body, originalModel, to)
    }
}

pctx := &proxyContext{
    body: body,  // potentially rewritten
    info: RequestInfo{
        Model: mappedModel,  // mapped name for logging + sticky
    },
}
```

**Key detail**: Update `Content-Length` is not needed because `BuildUpstreamRequest` uses `bytes.NewReader(body)` which sets the length correctly from the buffer.

### 2.4 Logging

Store both `originalModel` and `mappedModel` in `RequestLog`. Add a new field:

**File**: `internal/model/model.go` — in `RequestLog`:

```go
OriginalModel string `gorm:"default:''" json:"original_model"`
```

Only populated when a mapping was applied. This allows the admin UI to show "gpt-4o-mini → gpt-5.2" in logs.

### 2.5 Tests

**File**: `internal/proxy/extractor_test.go` — add:

- `TestReplaceModelInBody` — various JSON shapes, model at different positions.
- `TestReplaceModelInBody_NoMatch` — model not present, returns unchanged.
- `TestReplaceModelInGeminiPath` — standard path rewrite.

**File**: `internal/proxy/handler_test.go` — add:

- Integration test: mock store returns a mapping, verify upstream receives rewritten model.
- Test with no mapping: verify pass-through unchanged.

---

## Phase 3: Backend — Admin API

### 3.1 Admin store interface

**File**: `internal/admin/handler.go` — add to admin-local `Store` interface:

```go
ListModelMappings(ctx context.Context) ([]model.ModelMapping, error)
GetModelMapping(ctx context.Context, id string) (*model.ModelMapping, error)
CreateModelMapping(ctx context.Context, m *model.ModelMapping) error
UpdateModelMapping(ctx context.Context, m *model.ModelMapping) error
DeleteModelMapping(ctx context.Context, id string) error
```

### 3.2 Handler implementation

**New file**: `internal/admin/handler_model_mapping.go`

Five handlers following `handler_provider.go` patterns:

| Method | Handler | Notes |
|--------|---------|-------|
| `GET /admin/api/model-mappings` | `ListModelMappings` | Return all mappings |
| `POST /admin/api/model-mappings` | `CreateModelMapping` | Validate `id`, `from`, `to` required; `from` unique |
| `GET /admin/api/model-mappings/{id}` | `GetModelMapping` | 404 if not found |
| `PUT /admin/api/model-mappings/{id}` | `UpdateModelMapping` | Partial update |
| `DELETE /admin/api/model-mappings/{id}` | `DeleteModelMapping` | Use `handleDelete` helper |

Request/response types:

```go
type CreateModelMappingRequest struct {
    ID      string `json:"id"`
    From    string `json:"from"`
    To      string `json:"to"`
    Enabled *bool  `json:"enabled"`
}
```

### 3.3 Route registration

**File**: `internal/server/server.go` — add to `setupRoutes`:

```go
mux.Handle("GET /admin/api/model-mappings", auth.WrapFunc(adminHandler.ListModelMappings))
mux.Handle("POST /admin/api/model-mappings", auth.WrapFunc(adminHandler.CreateModelMapping))
mux.Handle("GET /admin/api/model-mappings/{id}", auth.WrapFunc(adminHandler.GetModelMapping))
mux.Handle("PUT /admin/api/model-mappings/{id}", auth.WrapFunc(adminHandler.UpdateModelMapping))
mux.Handle("DELETE /admin/api/model-mappings/{id}", auth.WrapFunc(adminHandler.DeleteModelMapping))
```

**File**: `internal/server/server.go` — update server-local `store` interface to include model mapping methods.

### 3.4 Tests

**New file**: `internal/admin/handler_model_mapping_test.go`

- CRUD lifecycle via HTTP (httptest).
- Validation errors (missing fields, duplicate `from`).
- 404 handling.
- Mock store following `mockStore` pattern in `handler_testutil_test.go`.

---

## Phase 4: Frontend — Admin UI

### 4.1 Types

**File**: `web/src/api/types.ts`

```typescript
export interface ModelMapping {
  id: string;
  from: string;
  to: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ModelMappingInput {
  id: string;
  from: string;
  to: string;
  enabled?: boolean;
}
```

### 4.2 API client

**File**: `web/src/api/client.ts` — add `createModelMappingsApi` factory:

```typescript
function createModelMappingsApi(request: AuthenticatedRequestFn) {
  return {
    list: () => request<ModelMapping[]>("/model-mappings"),
    get: (id: string) => request<ModelMapping>(`/model-mappings/${id}`),
    create: (data: ModelMappingInput) =>
      request<ModelMapping>("/model-mappings", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<ModelMappingInput>) =>
      request<ModelMapping>(`/model-mappings/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) =>
      request<void>(`/model-mappings/${id}`, { method: "DELETE" }),
  };
}
```

Add to `createApiClient` return object.

### 4.3 Hook

**New file**: `web/src/hooks/useModelMappings.ts`

Following `useProviders.ts` pattern — `useState` + `useEffect` + `useCallback` for refetch.

### 4.4 Page

**New file**: `web/src/pages/model-mappings/ModelMappings.tsx`

Simple table layout:

```
┌──────────────┬─────────────────┬─────────────────┬─────────┬──────────┐
│ ID           │ From            │ To              │ Enabled │ Actions  │
├──────────────┼─────────────────┼─────────────────┼─────────┼──────────┤
│ mini-to-5.2  │ gpt-4o-mini     │ gpt-5.2         │ ✓       │ ✏️ 🗑️    │
│ sonnet-remap │ claude-sonnet   │ claude-opus     │ ✓       │ ✏️ 🗑️    │
└──────────────┴─────────────────┴─────────────────┴─────────┴──────────┘
                                              [ + Add Mapping ]
```

- Create/Edit modal with `id`, `from`, `to`, `enabled` fields.
- Toggle enabled inline.
- Follow existing UI component patterns (Dialog, form elements from the Providers page).

### 4.5 Navigation

**File**: `web/src/App.tsx` — add route:

```tsx
<Route path="model-mappings" element={<ModelMappings />} />
```

**File**: `web/src/components/Layout.tsx` (or sidebar component) — add nav item "Model Mappings".

### 4.6 Tests

**New file**: `web/src/pages/model-mappings/ModelMappings.test.tsx`

- Renders mapping list.
- Create flow.
- Delete flow.
- Following existing test patterns (`@testing-library/react`).

---

## Phase 5: Tests & CI

### 5.1 Coverage targets

| Layer | Target | Notes |
|-------|--------|-------|
| Go store | 90%+ | CRUD + cache |
| Go admin | 90%+ | All endpoints + validation |
| Go proxy | 90%+ | Rewrite logic + integration |
| React | 40%+ | Page rendering + interactions |

### 5.2 Run order

1. `go test ./...` — all Go tests pass
2. `cd web && npm test` — all React tests pass
3. `cd web && npm run build` — frontend builds clean
4. `go build ./...` — backend compiles clean

---

## Implementation Order

```
Phase 1 (Backend Data) ──→ Phase 2 (Proxy Rewrite) ──→ Phase 3 (Admin API) ──→ Phase 4 (Frontend)
                                                                                       │
                                                                              Phase 5 (Final CI check)
```

Phases 1-3 are sequential (each depends on the prior). Phase 4 depends on Phase 3. Phase 5 is a final verification pass.

**Estimated file changes**: ~12 new files, ~8 modified files.
