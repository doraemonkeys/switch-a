# Request Logs WebSocket End-State Plan

## Status

✅ Completed

## Goal

- Replace overloaded request-log fields with one normalized end-state assessment.
- Make `service_outcome` the only reporting dimension for request/session outcome across WebSocket, HTTP, and SSE.
- Preserve historical rows without heuristic reinterpretation.

## Final Decisions

- No backward-compatibility layer for request-log semantics.
- Remove `success`, `status_code`, `error_msg`, `terminal_cause`, and `recovery_action` from `RequestLog`.
- Remove binary request statistics. Do not keep or emulate `success_count`, `fail_count`, or `success_rate`.
- Keep `RequestLog` as the final request/session assessment record.
- Keep `RequestAttempt` as provider-attempt evidence only.
- Keep `SessionCommitted` as the sticky boundary. Sticky writes do not move to completion.
- Post-visible `usage_limit_reached` remains `client_action = reconnect_required`; no transparent migration contract is claimed.
- Post-visible generic transport loss remains conservative: `client_action = none`, `service_outcome = unknown`.
- `websocket_connection_limit_reached` remains provider-native terminal evidence for the current socket: `client_action = none`, `service_outcome = completed`, no provider failure.
- `unknown` means "applicable but evidence insufficient". "Not applicable" is `NULL`.
- Structured evidence is drill-down only. Filters, aggregations, and badges must never depend on parsing JSON blobs.
- Add `semantics_version` so legacy and normalized rows are explicit.

## Canonical Assessment

One backend assessment path produces and persists the canonical end-state:

- `semantics_version`
- `client_transport_status_code`
- `completion_state`
- `service_outcome`
- `termination_actor`
- `termination_reason`
- `client_action`
- `session_evidence_json`
- WebSocket lifecycle facts that remain orthogonal:
  - `session_committed`
  - `client_visible`
  - `commit_source`

Assessment inputs:

- client-observed transport acceptance/status
- session committed
- session became client-visible
- completion observed
- final gateway terminal evidence
- final upstream handshake evidence
- final upstream post-upgrade evidence
- replaced/suppressed attempt evidence

Assessment rules:

- Derive from normalized facts first. Structured evidence refines classification; it does not replace facts.
- Accepted WebSocket sessions always persist `client_transport_status_code = 101`.
- Later upstream semantic events never overwrite `client_transport_status_code`.
- `service_outcome` is the primary reporting dimension.
- `client_action` is a client contract, not a reporting dimension.
- `completion_observed` is an explicit runtime fact and feeds assessment directly.

## Persistence Model

✅ Done

### RequestLog

Keep:

- `session_committed`
- `client_visible`
- `commit_source`

Add:

- `semantics_version`
- `client_transport_status_code`
- `completion_state`
- `service_outcome`
- `termination_actor`
- `termination_reason`
- `client_action`
- `session_evidence_json`

Remove:

- `success`
- `status_code`
- `error_msg`
- `terminal_cause`
- `recovery_action`

### RequestAttempt

Keep:

- provider-attempt identity, ordering, phase, and switch metadata
- existing attempt-scoped transport fields such as attempt `status_code` and `error`

Add:

- `attempt_evidence_json`

Rules:

- `RequestAttempt.status_code` remains provider-attempt transport evidence only.
- Replaced or suppressed attempt evidence stays on `RequestAttempt`.
- Final session/request evidence stays on `RequestLog`.
- Do not copy replaced-attempt evidence into the final `RequestLog`.

## Semantics Version

✅ Done

`semantics_version` is explicit on every row:

- `legacy_pre_assessment`
- `normalized_v1`

Rules:

- Pre-cutover rows remain `legacy_pre_assessment`.
- New rows write `normalized_v1`.
- Legacy rows are not heuristically reclassified.

## Nullability Contract

Required on normalized rows:

- `semantics_version`
- `client_transport_status_code`
- `completion_state`
- `service_outcome`
- `client_action`

Nullable on normalized rows:

- `termination_actor`
- `termination_reason`
- `session_evidence_json`

Rules:

- Use `NULL` when a field is not applicable to the protocol or the request shape.
- Use `unknown` only when the field is applicable but the evidence is insufficient.
- `termination_actor` and `termination_reason` stay `NULL` for nominal completions that do not need diagnostic terminal attribution.
- WebSocket-only lifecycle fields remain `NULL` on non-WebSocket rows.
- `session_evidence_json` and `attempt_evidence_json` stay `NULL` when there is no captured evidence.

## Enums

### completion_state

- `unknown`
- `incomplete`
- `completed`

### termination_actor

- `client`
- `gateway`
- `upstream`
- `internal`
- `unknown`

### termination_reason

- `provider_unavailable`
- `provider_configuration_error`
- `usage_limit_reached`
- `websocket_connection_limit_reached`
- `client_request_error`
- `client_disconnect`
- `transport_error`
- `upstream_semantic_error`
- `upstream_handshake_rejected`
- `client_upgrade_rejected`
- `internal_error`
- `clean_close`
- `unknown`

### client_action

- `none`
- `transparent_retry`
- `reconnect_required`

### service_outcome

- `completed`
- `interrupted`
- `never_started`
- `abandoned_by_client`
- `unknown`

## Outcome Rules

### service_outcome

- `never_started`: service never committed.
- `completed`: committed or accepted service ended with explicit completion, deterministic normal terminal evidence, or provider-native terminal completion that does not break continuity semantics.
- `interrupted`: client-visible service ended before completion and the user must actively reconnect to continue.
- `abandoned_by_client`: the client terminated the session or stream.
- `unknown`: transport or visibility ended without enough evidence to classify as completed or interrupted.

### client_action

- `transparent_retry`: only for pre-visible or pre-start replacement/retry that preserved client continuity.
- `reconnect_required`: only when all are true:
  - the session was client-visible
  - the failure is provider-scoped
  - user continuity is broken
  - the runtime cannot transparently migrate the active session
  - the terminal event is not `websocket_connection_limit_reached`
- `none`: all other cases.

### Explicit Error Rules

- `usage_limit_reached`
  - pre-visible or pre-start: provider-scoped failure; allow transparent retry or provider switch
  - post-visible: `client_action = reconnect_required`
  - post-visible: `service_outcome = interrupted`
  - auto-suspend only when provider usage-limit policy explicitly requires it

- `websocket_connection_limit_reached`
  - treat as provider-native terminal evidence for the current socket
  - pass provider evidence through to the client
  - `client_action = none`
  - `service_outcome = completed`
  - do not rewrite into gateway reconnect guidance
  - does not mark provider failure

- `client_disconnect`
  - `client_action = none`
  - `service_outcome = abandoned_by_client`
  - does not auto-generate reconnect guidance
  - does not mark provider failure

- generic post-visible `transport_error`
  - if completion was not observed, `service_outcome = unknown`
  - `client_action = none`
  - do not auto-promote to reconnect-required
  - does not mark provider failure by itself

## Non-WebSocket Mapping

- HTTP or SSE that finishes normally maps to `service_outcome = completed`.
- Failure before upstream service starts maps to `service_outcome = never_started`.
- Client-canceled request or stream maps to `service_outcome = abandoned_by_client`.
- Mid-flight transport loss without enough completion evidence maps to `service_outcome = unknown`.
- Non-WebSocket rows use the same `service_outcome` contract but keep WebSocket-only lifecycle fields `NULL`.

## Evidence Model

`session_evidence_json` and `attempt_evidence_json` use the same shape:

- `gateway`
  - terminal status code
  - terminal error code
  - terminal message snippet
- `upstream_handshake`
  - status code
  - body snippet
- `transport`
  - source
  - message snippet
  - is timeout
  - is client cancel
  - raw error snippet
- `upstream_event`
  - envelope type
  - provider error type
  - provider error code
  - status code
  - message snippet
  - raw payload snippet

Evidence budget:

- Every captured text fragment is UTF-8 safe and truncated to at most 512 bytes.
- Each evidence JSON blob is capped at 4096 bytes after serialization.
- Never persist unbounded raw payloads or raw errors.
- Redact secrets and credentials before persistence, including authorization headers, API keys, bearer tokens, and cookie values.

## Stats Contract

✅ Done

Replace binary stats with outcome-based stats.

Remove from backend model, store queries, admin API, and frontend:

- `success_count`
- `fail_count`
- `success_rate`

Normalized stats response must include:

- `total_requests`
- `avg_latency_ms`
- `outcome_counts`
- `requests_by_api_type`
- `requests_by_provider_outcome`
- `time_range`
- `outcome_timeseries`

Rules:

- Stats and time series aggregate `service_outcome`, not legacy booleans.
- Stats default to `semantics_version = normalized_v1`.
- Legacy rows are excluded from normalized stats and time series.
- If a future single-value KPI is needed, define it explicitly as a new metric such as `completed_rate`; do not reintroduce `success_rate`.

## Logs API and Filter Contract

✅ Done

Remove:

- `success` filter parameter

Add:

- `semantics_version`
- `completion_state`
- `service_outcome`
- `client_action`
- `termination_actor`
- `termination_reason`
- `client_transport_status_code`

Keep:

- protocol filters such as `is_websocket`
- WebSocket lifecycle filters that still represent orthogonal facts:
  - `session_committed`
  - `client_visible`
  - `commit_source`

Rules:

- Filters and badges must use normalized columns, not JSON parsing.
- Logs list may show both legacy and normalized rows.
- Legacy rows remain queryable by common metadata but are not reclassified into normalized semantic filters.

## Health Rules

- Health consumes assessment outputs and explicit failure-scope classification, not request-log booleans.
- `client_request_error` does not mark provider failure.
- `client_disconnect` does not mark provider failure.
- `websocket_connection_limit_reached` does not mark provider failure.
- generic post-visible `transport_error` does not mark provider failure by itself.
- `usage_limit_reached` suspends only when provider policy requires it.
- committed sessions that end by clean close or client disconnect remain non-failures for provider health.
- ✅ Done

## Backend Changes

- Refactor WebSocket logging into one assessment path that emits the canonical end-state object.
- Track `completion_observed` explicitly.
- Persist final session evidence on `RequestLog`.
- Persist replaced-attempt evidence on `RequestAttempt`.
- ✅ Done
- Replace all binary stats queries and time-series queries with `service_outcome` aggregations.
- Replace all top-level WebSocket log writes that use overloaded `status_code` with `client_transport_status_code`.
- Keep sticky writes anchored to `SessionCommitted`.
- ✅ Done
- Stop reading legacy semantic fields in assessment, stats, admin responses, and UI payloads in the same cutover.

## Frontend Changes

- Primary badge uses `service_outcome`.
- Secondary badges show `client_action` and `termination_reason`.
- Transport badge shows `client_transport_status_code`.
- Detail modal renders structured gateway, transport, handshake, and upstream evidence.
- Attempt timeline renders `attempt_evidence_json`.
- Stats cards and charts aggregate `service_outcome`.
- Logs filters use normalized semantic fields.
- Historical `legacy_pre_assessment` rows are rendered as explicit legacy rows, not reclassified heuristically.
- ✅ Done

## Migration

✅ Done

- Add columns:
  - `semantics_version`
  - `client_transport_status_code`
  - `completion_state`
  - `termination_actor`
  - `termination_reason`
  - `client_action`
  - `service_outcome`
  - `session_evidence_json`
  - `attempt_evidence_json`
- Write `semantics_version = normalized_v1` for all new rows at cutover.
- Tag pre-cutover rows as `legacy_pre_assessment`.
- Stop reading and writing legacy semantic fields in the same cutover.
- Do not backfill normalized semantics from legacy `success`, legacy `status_code`, or string parsing.
- Default normalized stats/time-series queries to `normalized_v1` only.
- After cutover, drop:
  - `success`
  - `status_code`
  - `error_msg`
  - `terminal_cause`
  - `recovery_action`

## Tests

✅ Done

- upgraded WebSocket session with later upstream semantic error keeps `client_transport_status_code = 101`
- post-visible `usage_limit_reached` maps to `client_action = reconnect_required`
- post-visible `usage_limit_reached` maps to `service_outcome = interrupted`
- post-visible generic `transport_error` maps to `client_action = none`
- post-visible generic `transport_error` without completion maps to `service_outcome = unknown`
- committed and visible `websocket_connection_limit_reached` maps to `client_action = none`
- committed and visible `websocket_connection_limit_reached` maps to `service_outcome = completed`
- `client_disconnect` maps to `service_outcome = abandoned_by_client`
- ✅ Done
- stats and time series aggregate `service_outcome` and do not expose `success_rate`
- normalized stats exclude `legacy_pre_assessment` rows by default
- logs API filters on `service_outcome`, `client_action`, and `termination_reason` without parsing evidence JSON
- final session evidence and replaced-attempt evidence are not conflated
- `SessionCommitted` still drives sticky writes
- ✅ Done
- ✅ Done
