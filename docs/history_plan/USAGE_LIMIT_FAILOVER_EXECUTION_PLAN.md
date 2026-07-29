# Usage Limit Failover Execution Plan

> Goal: exhausted providers must be suspended immediately, removed from continuity reuse, and handled with deterministic transparent failover or explicit reconnect-required termination.
>
> Status: Completed

## Problems

> The payload below is synthetic and contains no production identifiers or account usage data.

```json
{
    "id": 1001,
    "request_id": "00000000-0000-4000-8000-000000000000",
    "provider_id": "gpt-example-user",
    "api_type": "codex",
    "model": "gpt-5.4",
    "client_ip": "127.0.0.1",
    "user_id": "",
    "status_code": 101,
    "latency_ms": 100,
    "success": false,
    "is_sse": false,
    "is_websocket": true,
    "error_msg": "{\"type\":\"error\",\"error\":{\"type\":\"usage_limit_reached\",\"message\":\"The usage limit has been reached\",\"plan_type\":\"example\",\"resets_at\":0,\"eligible_promo\":null,\"resets_in_seconds\":0},\"status_code\":429,\"headers\":{\"X-Codex-Active-Limit\":\"codex\",\"X-Codex-Plan-Type\":\"example\",\"X-Codex-Primary-Used-Percent\":\"0\",\"X-Codex-Secondary-Used-Percent\":\"0\",\"X-Codex-Primary-Window-Minutes\":\"60\",\"X-Codex-Primary-Over-Secondary-Limit-Percent\":\"0\",\"X-Codex-Secondary-Window-Minutes\":\"1440\",\"X-Codex-Primary-Reset-After-Seconds\":\"0\",\"X-Codex-Secondary-Reset-After-Seconds\":\"0\",\"X-Codex-Primary-Reset-At\":\"0\",\"X-Codex-Secondary-Reset-At\":\"0\",\"X-Codex-Credits-Has-Credits\":\"False\",\"X-Codex-Credits-Balance\":\"0\",\"X-Codex-Credits-Unlimited\":\"False\"}}",
    "retry_count": 0,
    "is_sticky": true,
    "sticky_written": true,
    "session_committed": true,
    "terminal_cause": "upstream_semantic_error",
    "commit_source": "semantic_event",
    "created_at": "2026-01-01T00:00:00Z",
    "request_path": "/responses",
    "request_method": "GET",
    "user_agent": "",
    "request_id_header": "",
    "first_token_ms": null,
    "request_bytes": 1200,
    "response_bytes": 800,
    "content_type": "",
    "prompt_tokens": 100,
    "completion_tokens": 0,
    "total_tokens": 100,
    "attempts": [
        {
            "id": 2001,
            "request_id": "00000000-0000-4000-8000-000000000000",
            "provider_id": "gpt-example-user",
            "attempt": 0,
            "status_code": 101,
            "error": "failed to get reader: failed to read frame header: read tcp 127.0.0.1:28080-\u003e127.0.0.1:49000: wsarecv: An existing connection was forcibly closed by the remote host.",
            "phase": "visible",
            "outcome": "visible_session",
            "result_visible_to_client": true,
            "body_snippet": "{\"type\":\"error\",\"error\":{\"type\":\"usage_limit_reached\",\"message\":\"The usage limit has been reached\",\"plan_type\":\"example\",\"resets_at\":0,\"eligible_promo\":null,\"resets_in_seconds\":0},\"status_code\":429,\"headers\":{\"X-Codex-Active-Limit\":\"codex\",\"X-Codex-Plan-Type\":\"example\",\"X-Codex-Primary-Used-Percent\":\"0\",\"X-Codex-Secondary-Used-Percent\":\"0\",\"X-Codex-Primary-Window-Minutes\":\"60\",\"X-Codex-Primary-Over-Secondary-Limit-Percent\":\"0\",\"X-Codex-Secondary-Window-Minutes\":\"1440\",\"X-Codex-Primary-Reset-After-Seconds\":\"0\",\"X-Codex-Secondary-Reset-After-Seconds\":\"0\",\"X-Codex-Primary-Reset-At\":\"0\",\"X-Codex-Secondary-Reset-At\":\"0\",\"X-Codex-Credits-Has-Credits\":\"False\",\"X-Codex-Credits-Balance\":\"0\",\"X-Codex-Credits-Unlimited\":\"False\"}}",
            "latency_ms": 100,
            "created_at": "2026-01-01T00:00:00Z"
        }
    ]
}
```

- HTTP handshake-time `429 + usage_limit_reached` suspends the provider, but post-upgrade WebSocket semantic errors do not.
- WebSocket semantic parsing accepts `status` but may miss payloads that only expose `status_code`.
- Current selection metadata overloads one boolean with two meanings: continuity origin and retry suppression.
- Sticky invalidation is mostly lazy; provider suspension has no provider-scoped eager eviction path.
- Post-commit WebSocket sessions do not expose reconnect-required as a stable protocol or persistence contract.

## Target Behavior

1. The same usage-limit evidence produces the same provider lifecycle outcome in HTTP, WebSocket handshake, and WebSocket semantic phases.
2. Once a provider is exhausted, it is suspended until reset and cannot be reselected through sticky continuity or active fallback.
3. Pre-visible WebSocket provider-scoped failures may fail over transparently.
4. Once a WebSocket session becomes client-visible, provider-scoped failures must tell the client to reconnect instead of pretending silent recovery is possible.
5. The client-facing WebSocket shape stays the existing gateway error envelope; reconnect-required is expressed by a dedicated stable gateway error code and connection close.

## Contracts

- Visibility boundary:
  Pre-visible means no provider-specific upstream payload has become client-visible yet. It includes both `pre_accept` and `post_upgrade_pre_visible`.
  `client_visible=true` closes the transparent cross-provider failover window.

- Commitment boundary:
  `session_committed=true` marks the sticky, health, and logging lifecycle boundary.
  Do not infer `session_committed=true` from `client_visible=true`, or vice versa.

- Recovery action:
  Introduce an explicit recovery action domain value shared by classifier, orchestrator, persistence, and logs.
  Supported outcomes are `none`, `transparent_retry`, and `reconnect_required`.
  `recovery_action` is the request or session-level gateway recovery outcome, persisted on the final session row rather than on individual provider attempts.
  `transparent_retry` means the gateway absorbed at least one provider-scoped failure before the session became terminal for the client.
  `reconnect_required` is terminal-session-only and must not be emitted as an attempt-level switch reason.
  Existing attempt-level `switch_reason` stays responsible for explaining why one provider handoff occurred; runtime flags such as recovery-attempt bookkeeping remain internal orchestration state, not persisted source-of-truth lifecycle fields.
  Do not overload `terminal_cause` or free-form `message` with reconnect semantics.
  When the action is `reconnect_required`, emit the existing WebSocket `gateway_error` envelope with a dedicated stable error code, then close the connection.

- Quota reset evidence:
  Normalize quota-exhaustion evidence separately from transport shape.
  The canonical evidence model must carry `observed_at` plus reset-time candidates from HTTP headers, handshake bodies, and WebSocket semantic payload fields such as `resets_at`.
  When multiple valid future reset candidates exist, resolve the suspension deadline by taking the latest candidate. This rule must stay identical across HTTP, WebSocket handshake, and WebSocket semantic classification.
  WebSocket semantic errors must retain structured reset evidence instead of only `Raw`, so equivalent usage-limit failures resolve to the same suspension deadline across transport phases.

- Selection metadata:
  Split continuity origin from retry policy.
  Continuity origin answers where the provider came from, such as direct selection, sticky cache, or active registry.
  Retry policy answers whether another provider may be tried after failure.
  Sticky or active continuity is advisory before commitment, not an unconditional retry blocker.

- Suppressed upstream payload ownership:
  Once a pre-visible upstream payload is suppressed for transparent recovery, it becomes internal evidence.
  If recovery later terminates, the client-facing payload must come from the canonical gateway terminal result, not by replaying the raw suppressed upstream error.

- Post-visible WebSocket terminal delivery:
  Once `client_visible=true`, reconnect-required cannot rely on HTTP error paths or pre-accept upgrade failure handling.
  The gateway must have an explicit post-visible terminal write path that sends the canonical `gateway_error` event on the established socket and then closes it.

- Continuity eviction scope:
  Provider suspension must eagerly evict sticky cache entries through a provider-scoped reverse index.
  Provider-scoped eviction must be exposed through the continuity or sticky abstraction; suspension paths must not couple directly to a concrete `MemoryStickyCache`.
  The reverse index is part of the abstraction contract: `Set` replacement, `Delete`, and TTL cleanup must keep forward and reverse mappings consistent.
  Selector-side lazy validation stays as defense-in-depth.
  Active-request fallback must continue to honor provider availability checks at selection time.
  This change does not require provider-wide physical purging of active registry entries for correctness.

- Failure semantics to preserve:
  Preserve current HTTP and handshake handling for permanent errors and `usage_limit_reached`.
  Preserve current WebSocket semantic client-scoped versus provider-scoped identifier rules.
  Unify normalized evidence ingestion and provider lifecycle outcomes without collapsing distinct client-scoped failures into provider-scoped failover.

## Plan

### Phase 1. Explicit Recovery Contract

- Add a shared recovery action abstraction instead of encoding reconnect intent in `TerminalCause` or message text.
- Define recovery-action ownership explicitly: request/session level only, not provider-attempt level.
- Add a dedicated gateway error code constant for reconnect-required termination, carried in the existing WebSocket gateway error envelope.
- Keep `terminal_cause` focused on lifecycle termination and keep recovery action orthogonal.

Exit:

- reconnect-required has a stable internal and client-facing representation
- recovery action, attempt switch reason, and runtime recovery bookkeeping have non-overlapping ownership

### Phase 2. Selection Contract Refactor

- Replace the overloaded sticky boolean with structured selection metadata.
- Represent continuity origin explicitly.
- Decide retry eligibility from lifecycle phase and failure classification, not from continuity origin alone.
- Migrate the shared selection seam end-to-end: selector metadata, the HTTP or SSE first-attempt path, and the WebSocket orchestrator must all consume the same contract.

Exit:

- sticky-selected first attempts no longer block safe pre-visible failover by construction

### Phase 3. Canonical Failure Classification

- Extract one transport-agnostic failure classifier.
- Feed it normalized inputs from HTTP, WebSocket handshake, and WebSocket semantic events.
- Support both `status` and `status_code`.
- Normalize reset-time evidence alongside status evidence. The classifier input must carry `observed_at` and structured reset candidates from headers and payload fields such as `resets_at`.
- Resolve conflicting reset evidence by taking the latest valid future candidate, matching the current HTTP semantics and preserving one suspension rule across transport phases.
- Refactor WebSocket semantic upstream-error capture so quota evidence is preserved in structured fields rather than left only in `Raw`.
- Preserve current provider-scoped versus client-scoped semantics while producing switch reason, suspension deadline, and recovery action.
- Treat `usage_limit_reached` as provider-scoped quota exhaustion.

Exit:

- one classification path produces provider lifecycle, suspension deadline, and recovery decisions without changing unrelated 4xx behavior

### Phase 4. Provider-Driven Continuity Eviction

- Add provider-scoped sticky eviction topology so suspension can invalidate all affected sticky keys immediately.
- Add provider-scoped eviction capability to the continuity or sticky abstraction instead of reaching into a concrete cache implementation.
- Define reverse-index invariants explicitly: `Set` replacing an old provider, `Delete`, and TTL cleanup must all remove stale provider-to-key references.
- Trigger eager sticky eviction when a provider is suspended.
- Keep active-request fallback guarded by current availability checks so suspended providers cannot be reused there.
- Keep selector-side lazy validation only as a backstop.

Exit:

- a suspended provider cannot be reselected through sticky continuity or active fallback
- provider-scoped eager eviction cannot leak stale reverse-index entries

### Phase 5. WebSocket Recovery Rules

- Pre-visible provider-scoped failures: suppress and retry another provider.
- Client-visible provider-scoped failures: emit reconnect-required gateway error and close.
- Reuse the same recovery contract for usage-limit failures from handshake-time and post-upgrade semantic evidence.
- Implement an explicit post-visible terminal write path: after `client_visible=true`, write the canonical `gateway_error` event to the established socket, then close it.
- If a suppressed pre-visible provider-scoped failure later becomes terminal because recovery cannot succeed, finish with the canonical gateway terminal error envelope rather than replaying the raw suppressed upstream payload.

Exit:

- failover is transparent only before `client_visible=true`
- reconnect is explicit once the session is client-visible
- post-visible reconnect-required does not depend on pre-accept HTTP error handling
- suppressed provider-scoped payloads never leak as raw terminal client payloads after the gateway takes over recovery

### Phase 6. Persistence, Admin, and UI Rollout

- Add the new request/session lifecycle fields through the full persistence chain: model, schema migration, store queries, and API serialization.
- Extend log filtering and admin query parsing so the new lifecycle fields are queryable without overloading existing fields.
- Extend frontend API types and query builders so admin surfaces consume the new lifecycle fields explicitly.
- Keep historical rows migration-safe: nullable WebSocket-only lifecycle fields must not force fake values onto non-WebSocket rows.

Exit:

- the new lifecycle fields have one end-to-end persistence path from runtime result to DB row to admin API to frontend types

### Phase 7. Observability

- Persist canonical switch reason, lifecycle evidence, and recovery action.
- Keep `terminal_cause`, `commit_source`, `session_committed`, and `sticky_written` aligned with the new flow.
- Distinguish transparent failover from reconnect-required termination in logs without inferring it from message strings.

Exit:

- logs explain why the gateway switched, suspended, or required reconnect

## Tests

- HTTP `429 + usage_limit_reached` from headers only
- HTTP `429 + usage_limit_reached` from body only
- WebSocket handshake rejection with usage-limit evidence
- WebSocket semantic error with `status`
- WebSocket semantic error with `status_code`
- WebSocket semantic error with `resets_at` reaches the same suspension deadline as equivalent HTTP or handshake evidence
- conflicting reset evidence resolves to the latest valid future reset time across every transport phase
- recovery action persistence does not overload `terminal_cause`
- recovery action is stored only on the request/session lifecycle row, not duplicated as an attempt lifecycle field
- `client_visible` and `session_committed` remain independent lifecycle signals
- sticky-selected pre-visible failure still retries another provider
- sticky invalidation after suspension
- sticky reverse index stays correct across overwrite, delete, and TTL cleanup
- active fallback blocked by suspended provider
- pre-visible WebSocket failover on provider-scoped quota error
- client-visible WebSocket reconnect-required gateway error on provider-scoped quota error
- post-visible reconnect-required writes `gateway_error` to the established socket and then closes it
- suppressed pre-visible provider-scoped failure that later terminates emits canonical gateway error, not raw upstream payload
- client-scoped WebSocket semantic errors do not suspend the provider
- admin log query and frontend API types expose the new lifecycle field set end-to-end

## Acceptance

- equivalent usage-limit failures suspend the provider to the same reset time across transport phases
- the suspension deadline is always the latest valid future reset candidate from normalized evidence
- reconnect-required is represented by a stable recovery action and stable gateway error code
- recovery action has one request/session-level source of truth and does not duplicate attempt switch semantics
- `client_visible` closes transparent failover without implicitly setting `session_committed`
- sticky continuity and active fallback never reselect a suspended provider
- provider-scoped sticky eviction remains index-consistent under overwrite, delete, and expiry
- pre-visible WebSocket provider-scoped failures can fail over even when the first provider came from continuity
- client-visible provider-scoped failures require reconnect
- post-visible reconnect-required is delivered through the established WebSocket before close
- pre-visible suppressed upstream errors do not escape as raw terminal payloads once gateway recovery owns the session
- persistence, admin filtering, and frontend types stay aligned with the new lifecycle contract
- request logs make the outcome diagnosable without parsing free-form messages
