# Requirement Overview

## Relevant Subsystems / Modules

- `cmd/switch-a/main.go`: composition root that wires the SQLite store, cached store wrapper, selector, proxy server, and admin server. This remains the join point for admin CRUD, config transfer, and request-time routing enforcement.
- `web/src/App.tsx`, `web/src/pages/RoutingPolicies.tsx`, `web/src/hooks/useRoutingPolicies.ts`, `web/src/hooks/useGroups.ts`, `web/src/hooks/useProviders.ts`, `web/src/api/client.ts`, `web/src/api/types.ts`: the admin route (`/admin/routing`), its current CRUD contract, and the existing group/provider data sources the page must combine to support exact-provider targeting, provider-derived vendor choices, and rule enable/disable state.
- `internal/server/server.go`, `internal/admin/handler_routing_policy.go`, `internal/admin/handler_provider.go`, `internal/admin/handler_provider_response.go`: the HTTP seam for `/admin/api/providers`, `/admin/api/groups`, and `/admin/api/routing-policies`, plus the provider-delete path that must reject removal when an exact-provider rule still references that provider.
- `internal/admin/handler_config_export.go` and `internal/admin/handler_config_import.go`: current config transfer boundary. Routing policies are now part of the required export/import surface rather than an out-of-band admin-only feature.
- `internal/model/provider_state.go`, `internal/store/sqlite_routing_policy.go`, `internal/store/sqlite.go`, `internal/store/cached_store.go`: routing-policy domain model, schema migration, persistence, uniqueness enforcement, provider-reference integrity, and cached reads. The current model still only reflects group/vendor scopes and needs to absorb exact-provider and rule-level enabled state.
- `internal/selector/eligibility.go`, `internal/proxy/handler_select.go`, `internal/proxy/handler_websocket.go`, and `internal/proxy/websocket_session_orchestrator_selection.go`: runtime enforcement. Routing-policy resolution is shared across initial selection, sticky reuse, retry, active-request fallback, and websocket probe gating, so exact-provider rules, rule enable/disable semantics, and no-model behavior must be enforced here rather than only in admin CRUD.

## Likely Entry Points And Data Flow

- Admin users reach the feature through `web/src/App.tsx` at `/admin/routing`, where the page currently loads policies and groups. The clarified scope makes live provider data a first-class input because both exact-provider selection and allowed-vendor choices come from the current provider set.
- `/admin/api/providers` is the authority for provider IDs and vendor values. Exact-provider targeting selects one concrete provider, while vendor filtering can only offer existing non-empty provider `vendor` values from that same dataset.
- `/admin/api/routing-policies` remains the CRUD boundary. Its payload now needs to represent mutually exclusive targeting modes: either one exact provider, or the existing group/vendor filter mode, plus rule-level `enabled` state.
- `internal/admin/handler_routing_policy.go` is the backend gate for provider existence checks, provider-vs-group/vendor exclusivity, vendor validation against current providers, and disabled-rule persistence semantics. Disabled rules stay stored and still reserve the unique `(api_type, model_match_type, model_match_value)` key.
- `internal/store/sqlite_routing_policy.go` persists the routing-policy aggregate. Provider references become part of that persisted state, which means provider deletion must consult routing-policy usage and fail with `409 Conflict` when an exact-provider rule still points at the provider.
- `internal/admin/handler_config_export.go` and `internal/admin/handler_config_import.go` now sit on the same data path as providers, groups, settings, and routing policies. Routing-policy import must validate against a staged catalog first, then apply through one store-level transaction / unit-of-work so no partial state lands on failure.
- At request time, `internal/proxy/handler_select.go` builds `selector.ProviderSelectionEligibility`, and `internal/selector/eligibility.go` resolves the matching routing rule before filtering providers. Disabled rules must be ignored immediately for new connections, active exact-provider rules must narrow selection to that provider only, and an `api_type` with no active rules must fall back to normal selection.
- WebSocket selection reuses the same routing semantics. When websocket probe is disabled, the runtime must not perform compensating model discovery. When the request has no usable `model`, model-specific rules are treated as unmatched rather than producing a hidden-model gate.

## Important Conventions / Constraints

- Exact-provider targeting is atomic. A rule that selects one concrete provider may not also carry group or vendor filters; UI, API validation, persistence, and runtime semantics all need to enforce that mutual exclusivity.
- Allowed vendors are not free text. They must come only from existing providers whose `vendor` value is non-empty.
- Routing policy remains a hard constraint, not a hint, but only through active rules. If an `api_type` has no active rules, selection falls back to normal provider selection. If active rules exist and none match the request, selection fails closed instead of widening back to every provider for that `api_type`.
- Rule enable/disable is runtime-significant state. Disabled rules stay stored, editable, and recoverable, but must be ignored for new connections immediately and still occupy the `(api_type, model_match_type, model_match_value)` uniqueness key.
- If a group is referenced by a routing rule, deleting that group must fail with `409 Conflict` until the referencing rule is changed or removed.
- If a provider is referenced by an exact-provider routing rule, deleting that provider must fail with `409 Conflict` until the referencing rule is changed or removed.
- Matching semantics remain model-aware and ranked. Current support is `exact` and `prefix`, and the resolver already chooses the strongest match by rank and prefix length.
- When a request has no usable `model`, model-specific rules are treated as unmatched. WebSocket probe disablement is authoritative: runtime must not override it to recover hidden-model routing context.
- Routing policies are in scope for config export/import. Provider-scoped rules and rule enabled state must survive a full configuration round-trip together with the rest of admin-managed configuration.
- Config import must be all-or-nothing after staged validation; the apply phase cannot leave partial provider/group/settings/routing-policy writes behind.

## Likely Impact Areas

- Routing-policy domain modeling, referential integrity, and SQLite schema in `internal/model/provider_state.go`, `internal/store/sqlite_routing_policy.go`, and `internal/store/sqlite.go`.
- Admin API payloads and validation logic in `internal/admin/handler_routing_policy.go`, the provider delete flow in `internal/admin/handler_provider.go`, and config round-trip handlers in `internal/admin/handler_config_export.go` and `internal/admin/handler_config_import.go`.
- React routing-policy types, editor state, provider loading, and mutually exclusive targeting UX in `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/hooks/useRoutingPolicies.ts`, `web/src/hooks/useProviders.ts`, and `web/src/pages/RoutingPolicies.tsx`.
- Runtime selection logic in `internal/selector/eligibility.go` and every proxy path that reuses the shared eligibility closure through `internal/proxy/handler_select.go`.
- Tests across admin, store, selector, proxy, config export/import, and React. The clarified scope adds behavioral requirements around provider-reference conflicts, provider-derived vendor options, disabled-rule enforcement, and configuration round-trips.

## Notable Risks Or Unknowns

- Exact-provider rules need a stable provider identity that survives persistence, cache refreshes, and config round-trips. Any ambiguity between provider ID, name, or mutable display fields will leak into delete protection and import correctness.
- Vendor authority now comes from live provider data. If provider `vendor` values change casing, become blank, or diverge across imported configurations, vendor-scoped rules can silently narrow or become invalid unless normalization and validation are kept strict.
- Config import now has to preserve referential integrity between providers and routing policies. Import ordering and validation need to prevent rules from landing with dangling exact-provider references.
- The selector shares routing-policy resolution across sticky reuse, retry, fallback, and websocket probe gating. Any partial runtime wiring of exact-provider, enabled-state, or no-model semantics would create inconsistent policy behavior across entry points.
