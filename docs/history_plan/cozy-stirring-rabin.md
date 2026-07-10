# Plan: Observe requested effort and thinking configuration in request logs

## Scope

Record request-time reasoning controls for these HTTP endpoints only:

- Claude Messages (`/v1/messages`): `output_config.effort`, `thinking.type`, and
  `thinking.budget_tokens`.
- OpenAI Responses (`/responses`, `/v1/responses`): `reasoning.effort`.

This data is distinct from response-derived `reasoning_tokens`. Gemini, custom API types,
Claude count-tokens/models endpoints, and WebSocket/Realtime frame observation are unsupported.

## Domain model

The log must distinguish "not requested" from "could not be observed". Define the canonical
type in `internal/model` and reuse it in the proxy:

```go
type ReasoningObservationState string

const (
    ReasoningObservationCaptured   ReasoningObservationState = "captured"
    ReasoningObservationAbsent     ReasoningObservationState = "absent"
    ReasoningObservationInvalid    ReasoningObservationState = "invalid"
    ReasoningObservationAmbiguous  ReasoningObservationState = "ambiguous"
    ReasoningObservationUnsupported ReasoningObservationState = "unsupported"
)

const MaxReasoningValueRunes = 32

type RequestedReasoningObservation struct {
    State        *ReasoningObservationState `gorm:"column:reasoning_observation_state;type:text;default:null" json:"reasoning_observation_state,omitempty"`
    Effort       *string                    `gorm:"column:reasoning_effort;type:text;default:null" json:"reasoning_effort,omitempty"`
    Mode         *string                    `gorm:"column:reasoning_mode;type:text;default:null" json:"reasoning_mode,omitempty"`
    BudgetTokens *int64                     `gorm:"column:reasoning_budget_tokens;default:null" json:"reasoning_budget_tokens,omitempty"`
}
```

Embed `RequestedReasoningObservation` in `RequestLog` after `UsageDetails`, and use it as
`RequestInfo.Reasoning`. Do not add indexes until a query or filter uses these fields.

State semantics:

- `captured`: at least one supported field was captured from a valid, unambiguous document.
- `absent`: the supported endpoint had a valid document with none of the supported fields.
- `invalid`: malformed JSON, wrong field type, or an over-limit string. Successfully captured
  fields may remain populated, but invalid fields stay nil.
- `ambiguous`: a relevant object or member was duplicated. Retain the last successfully decoded
  value, while making the ambiguity explicit.
- `unsupported`: API type, endpoint, or WebSocket transport is outside this phase.
- nil state: legacy row whose observation state is unknown.

String values are stored exactly as decoded: no trimming, case folding, allowed-value filtering,
or silent truncation. Measure the limit with `utf8.RuneCountInString`; an over-limit value is not
stored and makes the observation `invalid`.

## Backend

1. **`internal/model/model.go`**
   - Add the state type, canonical observation type, rune limit, and embedded `RequestLog` field.
   - Do not add backend mode/tier constants that only the frontend would use.

2. **New `internal/proxy/reasoning.go`**
   - Add
     `ExtractRequestedReasoning(apiType, path string, body []byte) model.RequestedReasoningObservation`.
   - Dispatch only the endpoints listed in Scope; return `unsupported` otherwise.
   - Use `encoding/json.Decoder` to scan the complete top-level object. Materialize only
     `output_config`, `thinking`, or `reasoning` as `json.RawMessage`; skip other values with
     `skipJSONValue` so large message/input fields are not copied.
   - Do not stop after finding a target. Later duplicates replace earlier values and set
     `ambiguous`, matching ordered JSON decoding rather than silently becoming first-wins.
   - Scan each captured sub-object for its relevant members with the same last-wins/duplicate
     rule. Ignore unrelated members.
   - A syntax error sets `invalid`. Values decoded before the error may be retained. State
     precedence is `invalid` > `ambiguous` > `captured`/`absent`.
   - Non-object JSON and relevant members with the wrong JSON type are `invalid`.

3. **`internal/proxy/extractor.go`**
   - Add `Reasoning model.RequestedReasoningObservation` to `RequestInfo`.

4. **`internal/proxy/handler.go`**
   - Populate `RequestInfo.Reasoning` after buffering the body, passing both API type and path.

5. **`internal/proxy/handler_websocket.go`**
   - Set the observation state to `unsupported`: Realtime reasoning can change in client frames
     and must not be collapsed into an HTTP handshake value.

6. **Logging and storage**
   - Copy the canonical observation into `RequestLog` in both `logRequest` and
     `logWebSocketSession`.
   - Let `AutoMigrate` add the four nullable columns. Add an upgrade test proving an existing
     `request_logs` table receives them; keep `migration.go` for semantic/destructive migrations.

## Frontend

1. **`web/src/api/types.ts`**
   - Add `ReasoningObservationState` and the four optional nullable fields to `RequestLogBase`.

2. **New `web/src/components/logs/ReasoningBadge.tsx`**
   - Accept the four reasoning values explicitly rather than the complete log object.
   - Use a neutral pill for captured values; effort tiers are provider/model-dependent and are
     not status severity.
   - Prefer effort as the compact label, then mode, then budget. Preserve empty strings as `""`.
   - Use warning styling for `invalid` and `ambiguous`.
   - Render `—` for `absent`, `unsupported`, and legacy unknown, with a state-specific title.
   - Use CSS for visual truncation and keep the exact bounded value in the title.

3. **`web/src/components/logs/LogsTable.tsx`**
   - Add an **"Effort / Thinking"** column after Model and pass the four explicit values to
     `ReasoningBadge`.
   - Explain in `InfoTooltip` that this is requested configuration, not consumed reasoning tokens.

4. **`web/src/components/logs/constants.ts`**
   - Update `LOG_TABLE_COLUMNS` from 9 to 10 and update its comment.

5. **`web/src/components/LogDetailModal.tsx`**
   - Add separate rows for Observation, Effort, Thinking Mode, and Thinking Budget. Keeping the
     concepts separate avoids flattening them back into one display string.

## Tests

### Go

- `internal/proxy/reasoning_test.go`: Claude and OpenAI shapes; effort plus thinking; absent;
  unsupported API/path; non-object and malformed JSON; wrong types; exact whitespace retention;
  over-limit value becomes invalid without truncation; relevant top-level and nested duplicates
  use the last value and become ambiguous; trailing malformed data retains captured values but is
  invalid; target after a large leading field still extracts.
- Handler plumbing test: request body -> `RequestInfo.Reasoning` -> persisted `RequestLog`, including
  both `captured` and `absent` cases.
- `internal/store/sqlite_logs_test.go`: round-trip all four fields.
- Existing-schema migration test: `AutoMigrate` adds all four columns without changing old rows;
  their state remains null/unknown.

### React

- `ReasoningBadge.test.tsx`: captured effort/mode/budget, unknown future effort, empty string,
  absent, unsupported, invalid, ambiguous, and exact title text.
- `LogsTable.test.tsx`: column header/cell plus loading and empty-state `colSpan`.
- `LogDetailModal.test.tsx`: the four reasoning rows remain distinct.

Use role/title-based queries where applicable.

## Verification

1. Focused backend: `go test ./internal/proxy ./internal/store`.
2. Focused frontend: `cd web && pnpm exec vitest run src/components/logs/ReasoningBadge.test.tsx src/components/logs/LogsTable.test.tsx src/components/LogDetailModal.test.tsx`.
3. Frontend build: `cd web && pnpm build`.
4. Final gate: `make ci` (Go race/coverage/lint plus React coverage/typecheck/lint).

## Deferred

- Gemini and custom API types.
- OpenAI Realtime/WebSocket frame-level observation.
- Filtering or aggregation by effort.
- Claude `output_config.task_budget`, which is separate from `thinking.budget_tokens`.
