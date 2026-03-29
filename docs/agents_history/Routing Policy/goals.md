# Goals

## 1. Routing Policy Capability And Lifecycle Refactor
**Status:** in_progress
**Description:** Implement the full routing-policy requirement as one coordinated change across domain model, admin API, management UI, runtime enforcement, and regression coverage. The goal includes exact-provider targeting as an atomic scope, provider-derived vendor selection, rule enable/disable semantics that take effect immediately for new connections, preserved uniqueness for disabled rules, and end-to-end consistency across storage, admin flows, and request selection.