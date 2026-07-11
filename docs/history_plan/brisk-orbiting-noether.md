# Plan: First-class Grok (xAI) API type

## Scope

Add `grok` as a first-class API type for xAI's OpenAI-compatible Chat Completions
contract, end to end (proxy routing, admin validation, reasoning observation,
frontend constants). Token usage, SSE interception, first-token timing, auth
injection, and health tracking are already payload/contract-agnostic and need no
changes.

## Routing contract

Mirrors the codex dual-path precedent:

- `POST /chat/completions` and `POST /v1/chat/completions` → `grok`.
- `BuildUpstreamPath` strips the optional client-side `/v1` prefix so the provider
  `base_url` owns the API version segment (e.g. `https://api.x.ai/v1`).
- `GET /v1/models` stays claimed by `claude`; grok does not contest it. Model
  discovery for grok is available through the explicit namespace
  (`GET /grok/v1/models`, see [calm-braiding-hopper](calm-braiding-hopper.md)).
- WebSocket upgrades remain codex-only. Bare grok paths register POST only, so
  a genuine GET upgrade never reaches the proxy (mux catch-all 404); requests
  that do reach the handler with upgrade headers (POST, or the namespaced GET
  form) are rejected with 400.

## Backend

1. **`internal/proxy/router.go`** — `APITypeGrok`, `RouteGrokChatCompletions`,
   `RouteGrokChatCompletionsV1`, `ParseAPIType` branch, `/v1` strip in
   `BuildUpstreamPath`.
2. **`internal/server/server.go`** — register both POST routes (no GET: chat
   completions has no realtime/websocket form).
3. **`internal/admin/constants.go`** — `validAPITypes["grok"]`.
4. **`internal/proxy/reasoning.go`** — observe the Chat Completions top-level
   scalar `reasoning_effort`. The scanner previously only captured object members;
   `reasoningObjectKind` is generalized to `reasoningMemberKind` with a
   `reasoningEffortMember` scalar kind that reuses `captureString` semantics
   (null / wrong type / over-limit → `invalid`; duplicates → `ambiguous`,
   last-wins). Claude/Codex object handling is unchanged.
5. Model extraction (JSON `"model"` field), token usage (OpenAI-shaped `usage`
   with `completion_tokens_details.reasoning_tokens` and
   `prompt_tokens_details.cached_tokens`), SSE tail capture, and auth
   (`credential_type`-driven; auto mode resolves Bearer for OpenAI-SDK clients)
   already work generically — verified, no changes.

## Frontend

1. **`web/src/config/constants.ts`** — `API_TYPES.GROK`, `COMMON_API_TYPES`,
   `API_TYPE_OPTIONS` entry, `COMMON_VENDORS` gains `xai`. Forms, filters, and
   badges derive from these constants and need no per-component edits.
2. **`web/src/api/types.ts`** — extend `BuiltInAPIType` union.

## Tests

- Go: `ParseAPIType`/`BuildUpstreamPath` grok cases; `IsValidAPIType("grok")`;
  reasoning scalar shapes (captured/absent/invalid/ambiguous/over-limit/large
  leading field/codex-shaped object ignored); model extraction from body;
  handler-level persisted observation for `reasoning_effort`; websocket upgrade
  rejection on `/chat/completions`; mux-level route registration.
- React: constants length/content assertions extended to 4 API types.

## Deferred

- Chat Completions on other vendors than xAI (works today by pointing a `grok`
  provider's `base_url` at any OpenAI-compatible endpoint; no gateway change).
- `GET /models` model discovery for OpenAI-SDK clients (path owned by claude).
- WebSocket/Realtime for grok (xAI has no such contract).
