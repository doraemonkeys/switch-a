# Round 1 Plan Review

The plan is close, but it still leaves a few framework-level gaps around import atomicity, delete-path error propagation, and the config transfer contract.

## Findings

1. Major: Step 4 does not establish a staged or atomic validation boundary for config import, so an invalid routing policy can fail after earlier groups/providers/settings have already been written.
The plan says to import providers and groups before routing policies and then reuse the normal builder/validation path (`.agents/tasks/routing_policy_capability_and_lifecycle_refactor/plan.md:107-110`). The current import flow applies groups, providers, and settings incrementally with no enclosing transaction (`internal/admin/handler_config_import.go:92-104`, `internal/admin/handler_config_import.go:293-359`), and `buildRoutingPolicy` validates against live store state rather than an imported in-memory catalog (`internal/admin/handler_routing_policy.go:122-178`). As written, a later routing-policy validation failure would reject the request only after partial mutation. The plan needs either prevalidation against the imported catalog before any writes, or a store-backed transactional/staging path for the full import.

2. Major: The delete-conflict work is still framed as leaf-handler changes, but the current 500/conflict mapping is owned by shared delete infrastructure, and provider batch delete is a separate path.
Single-resource group/provider deletes funnel through `handleDelete`, which currently maps all delete errors to `500` (`internal/admin/handler.go:141-165`; `internal/admin/handler_group.go:212-220`; `internal/admin/handler_provider.go:622-649`). Provider batch delete bypasses that helper and wraps delete failures as a generic error string (`internal/admin/handler_provider_batch.go:170-178`). A plan that only names `handler_group.go` and `handler_provider.go` (`.agents/tasks/routing_policy_capability_and_lifecycle_refactor/plan.md:113-122`) risks landing the store-side integrity checks without the framework-level error propagation needed for `409 Conflict` on direct-reference violations and consistent behavior across delete entry points.

3. Minor: The config-transfer step does not call out the preview/result contract and UI surfaces that are hard-coded to providers/groups/settings.
The transfer structs currently define change/applied counts only for `providers`, `groups`, and `settings` (`internal/admin/config_transfer.go:116-154`), and the config import modal renders only those three buckets in both preview and applied results (`web/src/components/ConfigImportModal.tsx:265-279`; `web/src/components/ConfigImportModal.tsx:331-345`; `web/src/components/ConfigImportModal.tsx:584-589`). The step says routing policies join config export/import, but without explicitly extending these contract/UI surfaces the admin config workflow will underreport routing-policy changes even if handler serialization is added.

4. Minor: The plan changes the export format without calling for a config export version bump.
`ConfigExportVersion` is the compatibility gate (`internal/admin/config_transfer.go:22-23`), and imports reject mismatches (`internal/admin/handler_config_import.go:26-37`). Adding routing policies changes the meaning of a complete export. If the version stays at `2.0`, older builds can accept newer exports and silently ignore routing-policy data instead of failing fast.

**Verdict: 2 major, 2 minor valid issues.**
