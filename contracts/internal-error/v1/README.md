# Internal-error contract fixtures v1

These JSON files are executable cross-language contracts. Backend DTO tests and
frontend runtime-decoder tests must consume them from `unknown`; production code
must not import them as a catalog.

- `api-catalog.json` is the exact admin response; `api-catalog-internal.json`
  additionally freezes server-only routes, request/path policy, and error
  families.
- `rule-list.json` and `rule-stats.json` are exact response envelopes.
- `rule-mutations.json`, `reorder.json`, and `test-message.json` pair wire
  requests with their status, headers, and responses. Mutation and reorder
  cases branch from the revisions established by `rule-list.json`.
- `config-v4.json` is an exact export document. Derived stats, positions,
  timestamps, and revisions are intentionally absent from exported rules.
- `attempt-evidence-v2.json` contains named complete envelopes, including the
  required maximum-cardinality size proof.
- Protocol fixtures freeze root-only positive predicates and ordinary-output
  negatives. Body values are exact UTF-8 or base64 wire bytes.

Changing a wire shape requires a new versioned directory.
