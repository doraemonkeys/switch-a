# WebSocket Pre-Selection Model Probe Decisions

## Status

Completed on 2026-03-28.

## Purpose

This document defines a single runtime switch for WebSocket model probing in
`switch-a`.

The switch answers one architectural question only:

- May the proxy perform a replay-safe observation of the earliest client
  WebSocket business message before the initial provider selection when the
  handshake did not already expose a usable model and some pre-selection
  decision may consume that model?

This keeps the design explicit. Probe-on and probe-off are two distinct system
contracts, rather than a mix of sticky-only exceptions.

## Background

- Some upstream WebSocket protocols expose the effective request model only in
  client business messages, not in the HTTP handshake.
- `switch-a` is a transparent forwarder. It cannot require third-party clients
  to move that model into the handshake.
- In the current architecture, a pre-selection model probe can affect more than
  sticky precision. It can also affect provider selection before any upstream
  data becomes client-visible.
- Probe is an observation capability, not a license to consume and discard
  client business messages. If the proxy cannot preserve the same upstream-
  visible client message sequence that would exist without probing, then that
  WebSocket API does not support probing.
- Later semantic observation still exists for logging and diagnostics even when
  pre-selection probing is disabled. The runtime switch in this document only
  governs pre-selection probing.

## Definitions

### Handshake-Visible Model

A model identifier that is already available from the WebSocket handshake
request.

### Probed Model

A model identifier extracted from the earliest client WebSocket business
message before the initial provider selection.

### Pre-Selection Model Consumer

Any component that may consume the model before the initial provider decision.

Examples include:

- sticky continuity
- routing policy evaluation
- future model-aware provider filters or selector inputs

### Usable Model

A model value is usable when it is present and not in the proxy's unknown-model
sentinel state. Usability is evaluated using the same request-model semantics
that selection and sticky continuity already use.

### Replay-Safe Probe Path

A WebSocket API supports a replay-safe probe path only when the proxy can read
the earliest client business message for observation and still preserve the
upstream-visible client message sequence as if no probe had happened.

If the early-read messages cannot be replayed transparently, probing is not
supported for that API.

### Early-Read Window

A bounded implementation-defined duration during which the proxy may wait for
the earliest client business message before proceeding without a probed model.

The window must remain finite so probe-enabled mode does not stall server-first
or otherwise model-agnostic sessions indefinitely.

## Core Constraints

### Constraint 1: The Switch Must Control a Whole Pre-Selection Capability

The runtime switch must control whether pre-selection probing exists at all. It
must not pretend to disable probing while still letting hidden-model probing
silently affect provider routing through another path.

### Constraint 2: Selection Scope Is Determined by Information Available at Selection Time

Sticky continuity and provider selection must reflect only the model
information that was available before the initial provider decision was made.

Later semantic observation may enrich logs or diagnostics, but it must not
retroactively rewrite the selection scope that already happened.

### Constraint 3: Probe Failure Must Be Typed

"Did not observe a model" and "transport failed while probing" are different
runtime outcomes.

They must not collapse into the same behavior because one is an allowed
selection downgrade and the other is a session failure.

### Constraint 4: Probe Must Preserve Transparent Forwarding

Probe is permitted only as replay-safe observation.

The proxy must not read pre-selection client business messages unless the
current WebSocket API provides a replay-safe probe path. Otherwise the proxy
would stop being a transparent forwarder and would instead change protocol
semantics while claiming to only observe them.

## Decision

### Decision 1: Introduce One Runtime Switch

The project will use a single runtime config key:

- `websocket_probe_client_model`

Its meaning is exact:

- `true`: allow replay-safe pre-selection probing when the handshake did not
  already provide a usable model, at least one pre-selection model consumer
  exists, and the current WebSocket API supports a replay-safe probe path.
- `false`: do not read client business messages before the initial provider
  selection.

This key is part of the runtime-config contract and must be treated as a
stable backend/admin/frontend identifier during implementation.

The key authorizes probing. It does not force speculative probing when nothing
at selection time would consume the probed model.

### Decision 2: Handshake Model Always Wins

If the handshake already exposes a usable model:

- Do not pre-selection probe.
- Use the handshake-visible model directly for the current request.

The switch controls only hidden-model discovery. It does not add a second path
when the model is already known.

### Decision 3: Probe Enabled Is Demand-Driven by Pre-Selection Consumers

When `websocket_probe_client_model = true` and the handshake does not already
provide a usable model:

- Determine whether any pre-selection model consumer would use a hidden model
  for the current request.
- If no such consumer exists, skip probing entirely.
- The proxy may attempt a best-effort early read from the client connection
  before the initial provider selection only when the current WebSocket API
  supports a replay-safe probe path.
- If probing reveals a usable model, that model participates in the current
  request's pre-selection decisions.
- If probing does not reveal a usable model, the request continues with the
  unknown-model semantics already used by selection and sticky continuity.

This means probe-enabled behavior can affect:

- model-sticky precision
- model-aware routing policy resolution for the current request
- pre-visible provider switching and replay decisions that rely on the selected
  provider set

### Decision 4: Probe Disabled Removes All Pre-Selection Hidden-Model Discovery

When `websocket_probe_client_model = false`:

- Do not read client business messages before the initial provider selection.
- Initial provider selection may use only handshake-visible model information.
- If the handshake did not provide a usable model, the current request is
  selected under unknown-model semantics.

Consequences of probe-disabled mode:

- `model-sticky` degrades to `api_type` continuity when the handshake did not
  provide a usable model.
- Routing policies that depend on a model not visible in the handshake do not
  constrain the current request.
- Later observed model data may still update logs, metrics, or diagnostics, but
  it is non-authoritative for the already-completed selection decision.

### Decision 5: Unsupported Probe Capability Is Explicit

If probing is enabled and a pre-selection model consumer exists, but the
current WebSocket API does not provide a replay-safe probe path:

- Do not probe.
- Continue the current request under unknown-model semantics.
- Record this as an explicit unsupported capability outcome in diagnostics,
  distinct from "probe was bypassed" and distinct from "probe ran but did not
  find a usable model."

Probe support is capability-based, not API-name-based.

Current implementations may support probing for only some WebSocket APIs. A
future WebSocket API may participate once it provides a replay-safe adapter.

### Decision 6: No Retroactive Sticky Rewrite

If the initial provider selection happened without a usable model:

- Do not later rewrite that session into model-sticky just because a later
  frame exposed the model.
- Do not persist sticky state that claims model precision the selector did not
  actually have at selection time.

The continuity key used at selection time remains authoritative.

## Probe Outcome Taxonomy

### Outcome 1: Probe Bypassed

This applies when:

- the handshake already had a usable model
- the switch is off
- no pre-selection model consumer needs hidden-model discovery for the current
  request

Selection continues without pre-selection probing.

### Outcome 2: Probe Unsupported

This applies when probing is enabled, the handshake did not expose a usable
model, and a pre-selection model consumer exists, but the current WebSocket API
does not support a replay-safe probe path.

This is a capability mismatch, not a policy choice.

Selection continues with unknown-model semantics, and diagnostics should record
that probing was needed but unsupported.

### Outcome 3: Probe Completed Without a Usable Model

This applies when pre-selection probing ran but did not reveal a usable model
within the defined early-read window.

This is not a transport failure.

Selection continues with unknown-model semantics:

- `model-sticky` continuity degrades to `api_type`
- hidden-model routing policy does not apply to the current request

### Outcome 4: Probe Transport Failed

This applies when the client connection fails, closes, or the request context
is canceled while the proxy is still in the pre-selection probe step.

This is a terminal session failure, not a sticky downgrade.

The proxy must surface the failure through the ordinary WebSocket terminal
error path.

## Operational Model

### Shared Entry Rules

1. Inspect the handshake for a usable model.
2. If a usable model exists, use it directly and skip pre-selection probing.
3. If `websocket_probe_client_model = false`, skip pre-selection probing.
4. Determine whether any pre-selection model consumer would use a hidden model
   for the current request.
5. If no such consumer exists, bypass probing.
6. Verify whether the current WebSocket API provides a replay-safe probe path.
7. If probing is needed but unsupported, continue with unknown-model semantics
   and record an explicit unsupported-capability outcome.

### Probe Enabled

1. Attempt a best-effort early client-message probe before the initial provider
   selection only after the shared entry rules determine that probing is both
   needed and supported.
2. If a usable model is observed, use it for all current-request pre-selection
   consumers.
3. If no usable model is observed, continue selection with unknown-model
   semantics.
4. If the probe step fails at the transport level, terminate the session.

### Probe Disabled

1. Skip pre-selection probing entirely.
2. Perform the initial provider selection using handshake-visible information
   only.
3. If the handshake did not expose a usable model, continue with unknown-model
   semantics.

## Rejected Approaches

### Rejected: Sticky-Only Probe Semantics

This project will not describe the probe as a sticky-only optimization.

The probed model may be consumed by any pre-selection model consumer, including
sticky continuity, routing policy evaluation, and future model-aware selection
inputs.

If the system wants sticky-only semantics, the implementation must first remove
all non-sticky pre-selection consumers of the probed model.

### Rejected: Unsafe Observe-Then-Consume Semantics

This project rejects any design that reads pre-selection client business
messages without preserving transparent forwarding semantics.

If the current WebSocket API cannot provide replay-safe observation, the system
must treat probing as unsupported instead of silently consuming protocol data.

### Rejected: Untyped Probe Failure

The design rejects a single "probe failed" bucket.

The system must distinguish:

- no usable model observed
- probe transport failure

Those outcomes have different routing and session consequences.

### Rejected: Retroactive Selection Rewrite

Later model observation must not rewrite the continuity or routing scope that
was actually used during the initial selection.

## Recommended Default

Recommended initial default:

- `websocket_probe_client_model = true`

Why:

- It preserves the current pre-selection behavior by default.
- It avoids surprising operators with a silent loss of hidden-model-aware
  routing behavior.
- It still gives operators a simple, explicit kill switch when they want fully
  handshake-only selection semantics.

## Summary

The system exposes one explicit switch for one explicit capability:

- Probe enabled: the proxy may perform a replay-safe pre-read of the earliest
  client message to discover a hidden model before the initial provider
  selection when some pre-selection model consumer needs that model and the
  current WebSocket API supports probing.
- Probe disabled: the proxy performs initial selection from handshake-visible
  information only and never discovers hidden model data before selection.

This keeps the architecture simple and honest: the switch controls all
pre-selection hidden-model discovery, while late observation remains
non-authoritative metadata once selection has already happened.
