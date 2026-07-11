# Plan: Explicit API namespaces (/claude, /codex, /grok, /gemini)

## Scope

Let clients pin the API type in their base URL
(`http://gateway:28080/grok` → requests arrive as `/grok/chat/completions`)
instead of relying on bare contract-path sniffing. Bare paths cannot
distinguish two vendors that share a wire contract — every OpenAI-compatible
vendor claims `/chat/completions` — and `/v1/models` can only ever belong to
one type. Namespaces make the routing decision explicit per client.

This promotes the pre-existing `/gemini/` prefix and the `/custom/:toolId`
pattern into one uniform concept covering all built-in types.

## Semantics

- `/{claude|codex|grok|gemini}/<contract-path>` pins the API type; the
  namespace segment is gateway routing metadata and is **stripped before
  forwarding** — upstreams only ever see the native contract path.
- Codex and Grok additionally strip an optional leading `/v1` segment
  (segment-aware, so `/v1beta` stays intact); the provider `base_url` owns the
  API version. This generalizes the previous endpoint-specific strip and gives
  grok model discovery for free: `GET /grok/v1/models` → upstream `/models`.
- Bare contract paths keep working unchanged (native tool defaults).
- Namespaces register POST and GET; a namespaced WebSocket upgrade reaches the
  handler and gets the diagnostic 400 on non-codex types (bare grok paths stay
  POST-only, so a GET upgrade there still dies at the mux 404).
- Reasoning observation is defined over the contract path, so namespaced
  requests observe identically to bare ones.

## Behavior change (intentional)

`/gemini/*` previously forwarded the `/gemini` prefix to the upstream, which
no Google-native endpoint understands; it now strips to the native
`/v1beta/...` path. Upstreams that genuinely expect a `/gemini/*` path get it
back by ending their provider `base_url` with `/gemini`. Pre-v1.0, no
backward-compat consumers to protect.

## Touchpoints

1. `internal/proxy/router.go` — `builtinAPINamespaces`, `SplitAPINamespace`,
   `APINamespaceRoutePatterns`; `ParseAPIType` checks namespaces first;
   `BuildUpstreamPath` strips the matching namespace, then `trimVersionSegment`
   for codex/grok; `RouteGeminiPrefix` retired in favor of the namespace.
2. `internal/server/server.go` — register POST+GET for every namespace pattern.
3. `internal/proxy/reasoning.go` — normalize the namespace away before
   endpoint dispatch.
4. `web/src/config/constants.ts` — API type option descriptions mention the
   namespace form.
5. WebSocket dialing already flows through `BuildUpstreamPath` — no change.

## Tests

- Router: namespace parsing (incl. bare `/claude` and the `/claudex` negative),
  upstream stripping per type, gemini native-vs-namespaced equivalence,
  `/grok/v1/models` discovery.
- Server: mux-level POST/GET registration for all namespaces; websocket
  upgrade contract (bare grok 404 vs namespaced grok 400).
- Reasoning: namespaced claude/grok capture; namespaced count_tokens stays
  unsupported.
