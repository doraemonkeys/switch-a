# Routing Policy Capability And Lifecycle Refactor

Status: Completed

## Goal

Make routing policy a single, runtime-significant resource that:

- targets either one exact provider through `target_provider_id` or a group/vendor filter set
- can be enabled or disabled without releasing the unique `(api_type, model_match_type, model_match_value)` key
- behaves consistently across storage, admin CRUD, runtime selection, and config export/import

## Key Constraints

- Exact-provider mode is atomic: `target_provider_id` cannot coexist with groups or vendors.
- Allowed vendors come only from current providers with non-empty `vendor` values for the selected `api_type`; no free-text vendor creation.
- Disabled rules stay stored, editable, and re-enableable, and still occupy the unique key.
- Vendor filters are dynamic provider-property filters, not direct references.
- Direct references must be protected: deleting a referenced group or exact-target provider must fail with `409 Conflict`.
- Updating a provider must not remove an `api_type` still required by an exact-provider rule.
- Config import must validate against a staged imported catalog and apply atomically.
- If an `api_type` has no active rules at runtime, selection falls back to normal provider selection.
- If active rules exist for an `api_type` but the request matches none of them, selection fails closed.
- WebSocket probe disablement is authoritative. When the request has no usable `model`, model-specific rules are treated as unmatched instead of creating hidden-model demand.
- Config transfer identity is the natural key `(api_type, model_match_type, model_match_value)`, not storage-local IDs.
- If an existing rule already contains a vendor that no longer exists in the live provider catalog, non-vendor edits may preserve that normalized vendor set, but any vendor-set change must fully revalidate against the current catalog.

## Implementation Plan

### 1. Refactor the domain model and persistence shape ✅ Done

- ✅ Done: Move routing policy into its own model file so provider state and routing-policy lifecycle stop sharing one definition.
- ✅ Done: Add `Enabled bool` and `TargetProviderID *string`.
- ✅ Done: Keep group/vendor filters for filter mode, but normalize the resource so exact-provider rules cannot retain stale group/vendor scope.
- ✅ Done: Preserve the current uniqueness key regardless of enabled state.
- ✅ Done: Add SQLite migration support for `enabled` and `target_provider_id`, backfilling existing rows to `enabled = true`.
- ✅ Done: Expose two read paths:
  - ✅ Done: admin reads all rules
  - ✅ Done: runtime reads only active rules

### 2. Rebuild validation around one catalog-driven rule engine ✅ Done

- ✅ Done: Extend the admin payload with `enabled` and `target_provider_id`.
- ✅ Done: Centralize normalization and validation so CRUD and config import use the same rule semantics.
- ✅ Done: Validate:
  - ✅ Done: API type and model match fields
  - ✅ Done: exact-provider versus group/vendor mutual exclusivity
  - ✅ Done: provider existence and provider support for the selected `api_type`
  - ✅ Done: group existence
  - ✅ Done: vendor membership in the provider-derived vendor set
  - ✅ Done: filter mode requires at least one group or vendor
- ✅ Done: Keep `enabled` on the main resource contract instead of adding separate enable/disable endpoints.
- ✅ Done: On update, load the persisted rule first so unchanged stale vendors can survive lifecycle-only or other non-vendor edits.
- ✅ Done: Keep duplicate-key conflicts as `409`, even if the existing conflicting rule is disabled.

### 3. Enforce integrity across delete, update, and import flows ✅ Done

- ✅ Done: Introduce structured reference-conflict errors for routing-policy ownership of groups and exact-target providers.
- ✅ Done: Reject:
  - ✅ Done: deleting a group referenced by any routing policy
  - ✅ Done: deleting a provider referenced by `target_provider_id`
  - ✅ Done: updating a provider in a way that removes an `api_type` still required by an exact-provider rule
- ✅ Done: Surface those failures as `409 Conflict`, including provider batch delete results.
- ✅ Done: Include routing policies in config export/import and bump the export version so older builds fail fast instead of silently dropping data.
- ✅ Done: Export/import the full behavior shape: `enabled`, `target_provider_id`, group/vendor filters, and match metadata, but not storage-local IDs or timestamps.
- ✅ Done: Add a store-level import unit-of-work so the handler stops orchestrating cross-resource persistence through ad hoc CRUD calls.
- ✅ Done: Build a staged import catalog first, validate routing policies against that staged provider/group catalog, reject duplicate natural keys inside the import payload, then hand the fully staged payload to that unit-of-work for one transaction covering groups, providers, routing policies, and settings.
- ✅ Done: Extend config preview/apply summaries to include routing-policy counts.

### 4. Align runtime selection and admin UI ✅ Done

- ✅ Done: Update selector/runtime logic to evaluate only active rules.
- ✅ Done: Keep existing rule precedence: exact match over prefix, longer prefix over shorter prefix, API-type-only as fallback among active rules.
- ✅ Done: Exact-provider mode must narrow eligibility to one provider ID; filter mode keeps current group/vendor intersection semantics.
- ✅ Done: Runtime resolution semantics:
  - no active rules for an API type: fall back to normal provider selection
  - active rules exist but none match: fail closed
  - no usable request model: model-specific rules are unmatched
  - websocket probe disabled: do not perform compensating model discovery for routing-policy evaluation
- ✅ Done: Ensure sticky reuse, retry, active-request fallback, and websocket probe gating all use the same eligibility semantics.
- ✅ Done: Refactor the UI so the editor has explicit target modes:
  - ✅ Done: exact provider
  - ✅ Done: group/vendor filters
- ✅ Done: Load providers alongside groups and policies.
- ✅ Done: Replace free-text vendor entry with a provider-derived vendor picker.
- ✅ Done: When editing a rule with stale persisted vendors, keep them visible and removable so the UI does not silently mutate scope.
- ✅ Done: Show enabled/disabled state in the list and support toggling through the normal update flow.

## Acceptance Criteria

- A rule can persist either one `target_provider_id` or filter-mode groups/vendors, never both.
- Admin validation rejects unknown providers, unknown groups, unsupported provider/API type pairs, mixed scope, and newly introduced vendor values outside the provider-derived catalog.
- Disabled rules remain visible, editable, and unique-key-reserving.
- New runtime selections ignore disabled rules immediately.
- Exact-provider rules constrain normal selection, sticky reuse, retry, and fallback consistently.
- Deleting a referenced group or exact-target provider returns `409 Conflict`.
- Updating a provider cannot break an exact-provider rule by removing a required `api_type`.
- If an `api_type` has no active rules, runtime falls back to normal provider selection; if active rules exist but none match, runtime fails closed.
- If the request has no usable `model`, model-specific rules do not match and do not trigger websocket probing when probe is disabled.
- Config export/import preserves routing-policy behavior by natural key, includes routing-policy counts in preview/apply, and fails before mutation if the staged imported catalog is invalid.
- Config import applies through one store-level transaction / unit-of-work and leaves no partial writes on failure.
- Go and React tests pass with project coverage gates intact.
