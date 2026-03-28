# WebSocket Sticky Session Semantics Refactor — Improvement Plan ✅ Completed

## Context

The current WebSocket path already separates some concerns, but the boundary is still incomplete:

- Sticky is currently gated on `ConnectSuccess`, which is set after `relay()` completes (`websocket.go:186`). `ConnectSuccess` means "the upstream WebSocket dial succeeded and the relay ran to completion" — it does not distinguish whether the provider delivered meaningful service or immediately returned a semantic error. Health (`markSuccess`) uses the same condition (`handler_websocket.go:166-167`).
- Request log `success` is stricter: it requires `ConnectSuccess && Err == nil && UpstreamError == nil` (`handler_websocket.go:247-252`). A session with an upstream semantic error is therefore sticky-cached and marked healthy, but logged as unsuccessful.
- The relay layer in [`internal/proxy/websocket.go`](internal/proxy/websocket.go) correctly treats normal close and unexpected peer disconnect as transport-level outcomes, but it does not model whether the provider ever produced meaningful upstream service before the session ended.

This creates an architectural mismatch:

- `ConnectSuccess` lacks the granularity to distinguish "upstream dial succeeded but provider immediately emitted a semantic error" from "upstream dial succeeded and provider delivered real service." Sticky and health decisions based on this single bit are too coarse.
- Final clean shutdown is too strict to mean "successful session" for long-lived connections because client disconnects are normal.
- A single `success` boolean is being asked to represent handshake admission, service commitment, and terminal quality at the same time.

The result is exactly the ambiguity raised in review: a session can produce valid upstream data for a long time and still look like a failure only because the client disconnected near the end.

## Goals

1. Sticky must be based on session commitment, not terminal shutdown quality.
2. Client disconnect after committed upstream service must not retroactively negate session success.
3. Provider health must reflect provider-side failures, not normal client-side termination noise.
4. Logs and API responses must expose enough state to explain why a WebSocket session ended.
5. The refactor should replace overloaded booleans with explicit lifecycle semantics instead of layering more exceptions onto the current model.
6. Active fallback must share the same `SessionCommitted` boundary as sticky write, so an uncommitted provider is never reused through a different path.

## Design Decisions

### D1: Model WebSocket lifecycle explicitly

Replace the current implicit interpretation with three independent stages:

- `HandshakeAccepted`: the proxy upgraded client and established upstream WebSocket transport.
- `SessionCommitted`: the selected provider produced meaningful upstream service.
- `TerminalCause`: why the session ended.

These are orthogonal. A session may be accepted but never committed. A session may be committed and still end with a client disconnect. A session may be committed and later fail due to an upstream transport error.

### D2: Sticky boundary is `SessionCommitted`

Sticky must be written only after the provider has actually started serving the session, not merely after a successful HTTP 101 / WebSocket dial.

Target semantics:

- Upstream handshake succeeds, then provider immediately emits an auth/model/billing error: do not write sticky.
- Upstream sends valid business data, then client disconnects later: keep sticky.
- Upstream sends valid business data, then upstream fails later: keep sticky, but record failure via `TerminalCause`.

### D3: Commitment should be observer-driven, not byte-count-driven

The primary commitment signal should come from WebSocket semantic observers in [`internal/proxy/websocket_semantics.go`](internal/proxy/websocket_semantics.go), because "some upstream bytes existed" is weaker than "the provider emitted a meaningful non-error event."

#### Precise `SessionCommitted` definition

Three rules, evaluated in priority order:

1. **Observer present, parse succeeds**: committed when the observer sees `response.created` (provider began producing business output). Control-plane events (`session.created`, `session.updated`) do not count — they confirm configuration acceptance, not service delivery. Downstream events (`response.done`, `response.completed`) are redundant because `response.created` already triggered commitment.
2. **Observer absent** (non-Codex protocol): committed when the relay's upstream→client copy leg forwards the first upstream message frame to the client.
3. **Observer present, parse fails**: degrade to rule 2 (first-frame fallback). The observer must expose a parse-failure flag so the relay layer knows to activate the fallback path. This prevents an observer bug from permanently suppressing commitment for all sessions.

#### Implementation notes

`newWebSocketMessageObserver` returns `nil` for non-Codex protocols (`websocket_semantics.go:58`). Fallback commitment detection (rule 2) must therefore live in the relay layer (`websocket.go`), not in the observer. The upstream→client copy leg should set a `SessionCommitted` flag on `WebSocketResult` when it forwards the first upstream message and no semantic observer is present, or when the observer signals parse degradation.

This avoids conflating provider error envelopes with successful service.

### D4: Terminal cause must be explicit

Introduce a typed string constant instead of inferring meaning from `Err != nil`.

**Type placement**: `TerminalCause` and `CommitSource` are domain enumerations, not proxy implementation details. They must be defined in `internal/model` because they appear in `RequestLog` and are consumed by the store layer. The proxy package assigns values; it does not own the types. This avoids a circular dependency (`proxy → model` is allowed; `model → proxy` is not).

```go
// internal/model/terminal_cause.go

type TerminalCause string

const (
    TerminalUnknown                TerminalCause = "unknown" // historical rows where cause cannot be reconstructed
    TerminalCleanClose             TerminalCause = "clean_close"
    TerminalClientDisconnect       TerminalCause = "client_disconnect"
    TerminalUpstreamTransportError TerminalCause = "upstream_transport_error"
    TerminalUpstreamSemanticError  TerminalCause = "upstream_semantic_error"
    TerminalUpstreamHandshakeRejected TerminalCause = "upstream_handshake_rejected"
    TerminalClientUpgradeRejected  TerminalCause = "client_upgrade_rejected"
    TerminalInternalError          TerminalCause = "internal_error"
)

type CommitSource string

const (
    CommitSemantic        CommitSource = "semantic_event"
    CommitUpstreamMessage CommitSource = "upstream_message"
    CommitUnknown         CommitSource = "unknown"
)
```

This allows logs, health rules, and sticky rules to derive behavior from the same source of truth. Using a named type rather than raw `string` enables compile-time safety and makes the enum self-documenting.

### D5: No backward compatibility shortcuts

This project is pre-v1 and explicitly allows structural refactors. If current `RequestLog.Success` semantics are no longer defensible for WebSocket traffic, change the data model and API contract instead of preserving an ambiguous field shape.

### D6: Active fallback must share the `SessionCommitted` boundary

**Problem**: The current WebSocket path registers with `HasReceivedData = true` at handshake time (`handler_websocket.go:93`). Active fallback in `handler_select.go:34` trusts this flag to reuse a provider via `FindActiveProvider` (`active.go:275`). If only sticky write is moved to commit-based but active fallback still reads `HasReceivedData = true` from handshake, a provider that accepted the WebSocket upgrade but has not yet delivered meaningful service can still be reused by a concurrent request through the active fallback path.

**Fix**: Align WebSocket `HasReceivedData` with `SessionCommitted`, mirroring the HTTP path's `firstWriteResponseWriter` pattern:

- Register the WebSocket active request with `HasReceivedData = false` (instead of the current `true`).
- The relay layer signals commitment via a callback, not by touching the registry directly. The relay/forwarder is a pure transport layer (`websocket.go:129` explicitly scopes caller-side concerns outside); wiring `requestID` and `activeRegistry` into it would blur that boundary.
- **Callback mechanism**: The semantic observer already uses an `onUpdate` callback for model resolution. Add an `onCommit` callback with the same pattern. For protocols without a semantic observer, the relay layer accepts a lightweight `onFirstUpstreamMessage` callback. The handler wires these callbacks to call `activeRegistry.MarkDataReceived(requestID)`.
- Active fallback then naturally gates on committed sessions without any changes to `handler_select.go` or `active.go`.

This keeps the relay layer as a pure signal source ("commitment happened") while the handler owns the policy decision ("mark the active request as data-received").

## Proposed Data Model

### 1. Transport / proxy result model

Refactor [`internal/proxy/websocket.go`](internal/proxy/websocket.go):

- Rename or conceptually replace `ConnectSuccess` with `HandshakeAccepted`.
- Add `SessionCommitted bool`.
- Add `TerminalCause TerminalCause` (typed constant defined in D4).
- Add `CommitSource CommitSource` for diagnostics (typed constant: `CommitSemantic`, `CommitUpstreamMessage`, `CommitUnknown`).
- Keep `UpstreamError`, `CloseCode`, byte counters, and token usage.

Recommended shape (`TerminalCause` and `CommitSource` are imported from `internal/model`, as defined in D4):

```go
type WebSocketResult struct {
    HandshakeAccepted bool
    SessionCommitted  bool
    TerminalCause     model.TerminalCause
    CommitSource      model.CommitSource

    HandshakeStatusCode  int
    HandshakeBodySnippet string
    CloseCode            websocket.StatusCode
    Duration             time.Duration
    BytesClientToUpstream int64
    BytesUpstreamToClient int64
    Err                  error
    Model                string
    TokenUsage           *TokenUsage
    UpstreamError        *WebSocketUpstreamError
}
```

`Err` remains useful as raw evidence, but policy decisions should no longer be driven directly from `Err == nil`.

### 2. Semantic observation model

Extend [`internal/proxy/websocket_semantics.go`](internal/proxy/websocket_semantics.go):

- Add `SessionCommitted bool` to `WebSocketObservation` (runtime state, always known — not nullable unlike the persisted `RequestLog` field).
- Add `CommitEventType string`.
- Add observer logic that marks commitment only on meaningful upstream non-error events.

For Codex Realtime, the commitment event is:

- **`response.created`** — the provider began producing business output for the session.

Events that do **not** constitute commitment:

- `session.created` / `session.updated` — control-plane acknowledgments, not data-plane service.
- `response.done` / `response.completed` — redundant; `response.created` already triggered commitment.
- Client-originated events — never.
- Upstream error envelopes — never.
- Close frames — never.

The observer must also expose a `ParseDegraded bool` flag. If the observer encounters unparseable upstream frames, the relay layer activates the first-frame fallback path (D3 rule 3) rather than suppressing commitment entirely.

## Request Log / API Refactor

### 3. RequestLog fields

Refactor [`internal/model/model.go`](internal/model/model.go) so WebSocket outcomes are queryable without guessing from one boolean:

- Keep `IsSticky` with its existing semantics (whether the provider was **retrieved from** sticky cache for this request — read-side).
- Add `StickyWritten bool` to record whether provider affinity was **written** as a result of this session (write-side). This is a distinct concept from `IsSticky`.
- Add `SessionCommitted *bool` (nullable — `nil` for historical rows where commitment cannot be determined from legacy data; see Rollout Notes).
- Add `TerminalCause model.TerminalCause` (typed constant defined in D4, same package; defaults to `TerminalUnknown` for historical rows).
- Optionally add `HandshakeAccepted bool` if operational debugging needs it.

Recommended interpretation:

- `IsSticky`: whether this request was served from an existing sticky binding (read-side, unchanged).
- `StickyWritten`: whether this session caused a new sticky entry to be written (write-side, new field).
- `SessionCommitted`: whether upstream service meaningfully started.
- `Success`: whether the session avoided provider-side failure.

`Success` derivation for WebSocket (kept as a field for backward-compatible log queries in `sqlite_logs.go:166`):

```
Success = SessionCommitted && TerminalCause ∉ {upstream_semantic_error}
```

The only post-commit `TerminalCause` that negates success is `upstream_semantic_error` (provider actively reported a business-layer failure). Transport errors after committed service are protocol noise, not provider failure — the same reasoning as the health matrix in Section 6.

Resulting behavior:

- Client disconnect after valid upstream data: `Success = true`.
- Clean close after valid upstream data: `Success = true`.
- Upstream transport error after valid upstream data: `Success = true`, `StickyWritten = true`.
- Upstream semantic error after valid upstream data: `Success = false`, `StickyWritten = true`.
- Immediate upstream semantic failure after handshake: `Success = false`, `StickyWritten = false`.
- Handshake rejected: `Success = false`, `StickyWritten = false`.

### 4. Storage / migration

Update the SQLite schema and migration path in the store layer to persist the new fields. Because this is pre-v1, prefer an explicit migration that makes the new semantics first-class rather than hiding them in `usage_details` or `error_msg`.

## Runtime Policy Changes

### 5. Sticky write and active fallback policy

Refactor [`internal/proxy/handler_websocket.go`](internal/proxy/handler_websocket.go):

- Remove the current `if result.ConnectSuccess { UpdateStickyWithTTL(...) }` policy.
- Replace it with `if result.SessionCommitted { UpdateStickyWithTTL(...) }`.

This is the core behavior change requested by review.

Align active fallback as specified in D6:

- Change WebSocket active request registration to `HasReceivedData = false` (currently `true` at `handler_websocket.go:93`).
- Wire the `onCommit` callback (from the observer or relay layer) to call `activeRegistry.MarkDataReceived(requestID)` in the handler. The relay layer itself does not touch the registry — it only fires the callback.
- No changes to `handler_select.go` or `active.go` — they already gate on `HasReceivedData`.

### 6. Health policy

**Design principle**: health measures "can this provider serve the next request?", not "did this session end cleanly?" WebSocket long-lived connections routinely terminate with transport errors on either side (timeouts, network jitter, client closing tabs), yet the same provider handles the next request without issue. Treating these as failures would feed false positives into the circuit breaker.

The health decision is **single-shot**: one `markSuccess` or one `markFailure` at session end, derived from `SessionCommitted` × `TerminalCause`. The current `MarkSuccess` / `MarkFailure` interface does not need to change.

#### Explicit judgment matrix

| Committed? | TerminalCause | Health action | Rationale |
|---|---|---|---|
| No | `upstream_handshake_rejected` | `markFailure` | Provider refused connection |
| No | `upstream_transport_error` | `markFailure` | Provider unreachable |
| No | `upstream_semantic_error` | `markFailure` | Provider config/auth mismatch |
| No | `client_upgrade_rejected` | — (no call) | Client-side problem, provider not involved |
| No | `internal_error` | — (no call) | Proxy bug, not provider fault |
| Yes | `clean_close` | `markSuccess` | Normal completion |
| Yes | `client_disconnect` | `markSuccess` | Provider was fine; client left |
| Yes | `upstream_transport_error` | `markSuccess` | Provider already served successfully; transport errors at session end are protocol noise |
| Yes | `upstream_semantic_error` | `markFailure` | Provider actively reported a business-layer error after having started service |
| Yes | `internal_error` | `markSuccess` | Proxy bug, not provider fault |

This aligns the WebSocket path with the existing HTTP/SSE principle already used in [`internal/proxy/handler.go`](internal/proxy/handler.go): client-side disconnect after upstream success should not poison provider health. It goes further by also treating post-commit upstream transport errors as noise — the operational evidence is that these are normal for WebSocket lifecycles and the next request succeeds.

**Explicit tradeoff**: treating all post-commit `upstream_transport_error` as `markSuccess` means the circuit breaker will never react to a provider that systematically drops the socket immediately after `response.created`. This is a deliberate choice: the overwhelming majority of post-commit transport errors are normal lifecycle noise, and adding duration/byte thresholds to distinguish "real early drops" from "normal closes" would be premature complexity. If this degradation pattern emerges operationally, a duration-gated refinement can be added as a future iteration — but it should be driven by observed data, not speculative edge cases.

### 7. Log success policy

Replace `websocketLogSuccess` in [`internal/proxy/handler_websocket.go`](internal/proxy/handler_websocket.go) with a lifecycle-based helper that derives success from `SessionCommitted` plus `TerminalCause`, not from "the session ended with no error object."

## Implementation Phases

### Phase 1: Core lifecycle refactor ✅ Done

Files:

- `internal/model/terminal_cause.go` ← **new file**: `TerminalCause` and `CommitSource` type definitions (D4)
- `internal/proxy/websocket.go`
- `internal/proxy/websocket_semantics.go`

Changes:

- Define `TerminalCause` and `CommitSource` in `internal/model` so both proxy and store layers can consume them without circular imports.
- Extend `WebSocketResult` to use `model.TerminalCause` and `model.CommitSource`.
- Extend `WebSocketObservation` with `SessionCommitted`, `CommitEventType`, and `ParseDegraded`.
- Add commitment detection in the semantic observer: trigger on `response.created` only (Codex path).
- Add fallback commitment detection in the relay layer's upstream→client copy leg for protocols without a semantic observer, or when the observer signals `ParseDegraded`.
- Classify terminal cause from relay error outcomes and propagate it out of the relay layer.
- Add `onCommit` callback support: the semantic observer fires it on `response.created`; the relay layer fires `onFirstUpstreamMessage` for non-observer protocols or when `ParseDegraded` is true. Neither callback touches handler-side state directly — they are invoked by the handler to bridge the signal (D6).

Why this phase comes first:

The handler cannot apply correct sticky/health rules until the relay returns the right domain information.

### Phase 2: Handler policy rewrite ✅ Done

Files:

- `internal/proxy/handler_websocket.go`

Changes:

- Change active request registration to `HasReceivedData = false` (D6).
- Wire the `onCommit` / `onFirstUpstreamMessage` callbacks (Phase 1) to call `activeRegistry.MarkDataReceived(requestID)`. The handler owns this policy; the relay layer only signals the event.
- Replace handshake-based sticky writes with commitment-based writes.
- Replace connect-success-based health updates with the explicit judgment matrix from Section 6.
- Replace final-error-based log success calculation with lifecycle-aware calculation.

Why this phase is separate:

Policy should consume a coherent lifecycle model, not recompute transport heuristics inside the handler.

### Phase 3: Persistence and admin/log API ✅ Done

Files:

- `internal/model/model.go`
- `internal/store/sqlite.go`
- `internal/store/migration.go`
- Any log query / admin API serializers that expose `RequestLog`

Changes:

- Persist `SessionCommitted`, `StickyWritten`, and `TerminalCause`.
- Add migration logic with correct historical defaults (see Rollout Notes).
- Ensure filters and JSON responses expose the new fields.

Why this phase matters:

Without persisted lifecycle fields, operators still cannot distinguish "client disconnected after valid service" from "provider failed before service began."

### Phase 4: UI / diagnostics alignment ✅ Done

Files:

- Frontend log/request types and detail views as needed

Changes:

- Show terminal cause for WebSocket sessions.
- Display committed-vs-uncommitted state when useful.
- Avoid presenting a committed client-disconnect session as a generic failure.

Why this is not optional:

If backend semantics improve but UI still compresses everything into one red failure badge, the operational confusion remains.

## Testing Plan

### Unit tests

Add or update tests in:

- `internal/proxy/websocket_test.go`
- `internal/proxy/websocket_semantics_test.go`
- `internal/proxy/handler_websocket_test.go`

Required cases:

1. **Handshake accepted, immediate upstream semantic error, no business event:**
   - `SessionCommitted = false`, `StickyWritten = false`, `Success = false`
   - Health: `markFailure`
   - Active fallback: `HasReceivedData` remains `false`

2. **Upstream sends valid event, then client disconnects:**
   - `SessionCommitted = true`, `StickyWritten = true`, `Success = true`
   - Health: `markSuccess`
   - Active fallback: `HasReceivedData = true` (set at commit)

3. **Upstream sends valid event, then upstream transport error:**
   - `SessionCommitted = true`, `StickyWritten = true`, `Success = true`
   - Health: `markSuccess` (transport errors after committed service are protocol noise)
   - This is distinct from case 8 (semantic error after commit)

4. **Client upgrade rejected before upstream dial:**
   - `SessionCommitted = false`, no health call, no sticky write

5. **Protocol without semantic observer, upstream message forwarded:**
   - Fallback commitment path triggers `SessionCommitted = true`
   - `CommitSource = "upstream_message"`

6. **Uncommitted session must not be reused via active fallback:**
   - Register with `HasReceivedData = false`
   - Concurrent request's `FindActiveProvider` must not return this provider
   - After commitment, `MarkDataReceived` fires and the provider becomes visible

7. **Observer parse failure degrades to first-frame fallback:**
   - Observer signals `ParseDegraded = true`
   - First upstream message frame triggers commitment
   - Session is not permanently suppressed from commitment due to observer bug

8. **Upstream sends valid event, then upstream semantic error:**
   - `SessionCommitted = true`, `StickyWritten = true`, `Success = false`
   - Health: `markFailure` (provider actively reported business-layer failure)

9. **Committed session, client disconnects — sticky is preserved:**
   - Verify sticky entry survives even though the session did not end with clean close

10. **Uncommitted session — no sticky write regardless of TerminalCause:**
    - Handshake accepted but no business event → `StickyWritten = false` for every TerminalCause variant

### Store / migration tests

Add migration coverage for new `RequestLog` fields and verify read/write round-trips.

### Integration tests

Exercise end-to-end WebSocket forwarding scenarios with a fake upstream:

- clean close
- client disconnect
- upstream error event
- upstream transport reset after partial data

## Rollout Notes

- Because sticky semantics change from handshake-based to commit-based, expect fewer false-positive sticky entries for sessions that fail immediately after connection establishment.
- Existing historical logs will not be fully normalized unless backfilled. That is acceptable; the important change is to make new records semantically correct.
- Migration defaults for historical rows:
  - `session_committed`: nullable. Set `true` where `Success = true` (if the old success criteria passed, the session definitely committed). Set `NULL` where `Success = false` — the old `Success` definition (`ConnectSuccess && Err == nil && UpstreamError == nil`) is stricter than `SessionCommitted`, so `Success = false` does not prove the session never committed. `NULL` means "cannot be determined from historical data."
  - `terminal_cause`: set to `"unknown"` (`TerminalUnknown`) for all historical rows. This is a first-class enum value (D4), not an out-of-band sentinel — the column stays NOT NULL and every row has a valid typed value.

## Acceptance Criteria

1. WebSocket sticky is written only after committed upstream service.
2. Active fallback does not reuse a provider that has not yet committed — `HasReceivedData` is `false` until `SessionCommitted`.
3. Client disconnect after committed service does not appear as a failed session.
4. Post-commit upstream transport errors are treated as protocol noise: `markSuccess`, `Success = true`.
5. Post-commit upstream semantic errors are treated as provider failure: `markFailure`, `Success = false`.
6. Provider-side failures remain visible even when they happen after commitment (sticky is preserved, but log records the failure).
7. Logs can distinguish at least these cases:
   - handshake rejected
   - committed then clean close
   - committed then client disconnect
   - committed then upstream transport error
   - committed then upstream semantic error
   - never committed due to upstream semantic error
8. Observer parse degradation falls back to first-frame commitment instead of permanently suppressing commitment.
9. `TerminalCause` and `CommitSource` types live in `internal/model`, not `internal/proxy`.
10. The implementation removes the overloaded "one boolean means everything" design rather than adding more special-case branches around it.
