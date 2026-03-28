# WebSocket Support for Logging & Monitoring UI

## Context

Commit `47c93ff` added WebSocket proxy for OpenAI Realtime API. The proxy layer correctly populates `RequestLog.IsWebSocket` and `ActiveRequest.IsWebSocket`, but the **query/filter chain** (backend) and **display layer** (frontend) were never updated. WebSocket connections currently appear as indistinguishable "Regular" HTTP requests in logs and monitoring.

## Changes Overview

### Backend (3 files)

**1. `internal/model/model.go`** — Add `IsWebSocket *bool` to `LogFilter`
- Add field after `IsSSE *bool` (line ~201), mirroring the same `*bool` pattern
- This is a query filter struct, not a domain model — two independent boolean filters mapping to two DB columns is the most orthogonal design

**2. `internal/store/sqlite_logs.go`** — Add SQL clause in `applyLogFilters`
- Add `is_websocket` WHERE clause after the `is_sse` block (after line ~73), identical pattern:
  ```go
  if filter.IsWebSocket != nil {
      query = query.Where("is_websocket = ?", *filter.IsWebSocket)
  }
  ```

**3. `internal/admin/handler_logs.go`** — Parse `is_websocket` query param
- Add parsing after `is_sse` line (~108), reusing existing `parseBoolPtr` helper:
  ```go
  filter.IsWebSocket, errMsg = parseBoolPtr(getQueryParam(query, "is_websocket"), "is_websocket")
  ```

### Frontend (10 files)

**4. `web/src/api/types.ts`** — Add `is_websocket` field to 3 interfaces
- `ActiveRequest`: add `is_websocket: boolean` after `is_sse` (line ~200)
- `RequestLog`: add `is_websocket: boolean` after `is_sse` (line ~267)
- `LogFilter`: add `is_websocket?: boolean` after `is_sse` (line ~314)

**5. `web/src/api/client.ts`** — Serialize `is_websocket` in `buildLogsQuery`
- Add `is_websocket` serialization after the `is_sse` line (~90), mirroring the same pattern:
  ```ts
  if (filter?.is_websocket != null) query.set("is_websocket", String(filter.is_websocket));
  ```
- Without this, the filter UI silently never sends the param to the backend — the entire filter feature would be non-functional

**6. `web/src/components/LiveRequestsPanel/RequestRow.tsx`** — Add WS badge (4 locations)
- Use **emerald/green** color scheme (`bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300`) to visually distinguish from SSE (purple)
- `CompactRow` (~line 291): add `else if (request.is_websocket)` block with "WS" badge after SSE badge
- `FullRow` (~line 370): same pattern
- `RequestDetailPanel` (~line 83): same pattern
- Tooltip (~line 453): extend to `request.is_websocket ? " (WS)" : request.is_sse ? " (SSE)" : ""`
- **Skip long-running indicator for WS**: in duration calculation (~line 443), set `isLongRunning = false` when `request.is_websocket` — WS connections are inherently long-lived, the 5-minute threshold doesn't apply

**7. `web/src/components/LiveRequestsPanel/utils.ts`** — Suppress long-running indicator for WS
- The `isLongRunning` computation also lives here; patch it to return `false` when `is_websocket` is true, same rationale as RequestRow

**8. `web/src/components/LogFilters.tsx`** — Expand Request Type dropdown + `hasActiveFilters`
- Replace the current 3-option dropdown with 4 options. The value becomes a composite string that maps to `is_sse` + `is_websocket` params:
  - `""` → All Types (both undefined) — badge: none
  - `"sse"` → SSE Stream (`is_sse: true`) — badge: "SSE"
  - `"ws"` → WebSocket (`is_websocket: true`) — badge: "WebSocket"
  - `"regular"` → Regular (`is_sse: false, is_websocket: false`) — badge: "Regular"
- Update `onChange` handler to set both `is_sse` and `is_websocket` based on selection
- Update active filter badge to show the label matching the selected state above
- **Update `hasActiveFilters` helper**: add `filter.is_websocket !== undefined` to the boolean check so the "Clear Filters" button appears when WS filter is active

**9. `web/src/pages/Logs.tsx`** — Update `hasActiveFilters` and `handleClearFilters`
- **`hasActiveFilters`** (~line 60): add `filter.is_websocket !== undefined` to the boolean check, matching the LogFilters update
- **`handleClearFilters`** (~line 84): add `is_websocket: undefined` to the reset object so clearing filters properly resets the WS filter

**10. `web/src/components/logs/LogsTable.tsx`** — Add WS badge in API column
- After SSE badge block (~line 282), add WS badge with emerald color and `title="WebSocket connection"`:
  ```tsx
  {log.is_websocket && (
    <span className="..." title="WebSocket connection">WS</span>
  )}
  ```

**11. `web/src/components/LogDetailModal.tsx`** — Update Request Type display
- Expand the ternary at lines 261-274 to three cases: `is_websocket` → emerald "WebSocket" badge, `is_sse` → purple "SSE Stream", else → gray "Regular"
- Update `TransferStats` labels for WS: pass `requestLabel`/`responseLabel` props to `TransferStats` (e.g. `"Sent"`/`"Received"` for WS, default `"Request"`/`"Response"` otherwise)

**12. `web/src/components/TransferStats.tsx`** — Generic label props
- Replace the `isWebSocket?: boolean` approach with generic `requestLabel?: string` and `responseLabel?: string` props on `TransferStatsProps` — this is more reusable than coupling to a specific protocol flag
- Default to `"Request"` / `"Response"` when props are omitted
- Update the visual flow indicator label from "Server" to "Upstream" when `responseLabel` is `"Received"`

**13. `web/src/pages/Monitor.tsx`** — Suppress long-running indicator for WS
- The `isLongRunning` computation also lives in this page; patch it to return `false` when `is_websocket` is true, consistent with RequestRow and utils.ts

## Files Modified (Summary)

| Layer | File | Change |
|-------|------|--------|
| Backend | `internal/model/model.go` | +1 field in `LogFilter` |
| Backend | `internal/store/sqlite_logs.go` | +3 lines in `applyLogFilters` |
| Backend | `internal/admin/handler_logs.go` | +1 line in `parseLogFilter` |
| Frontend | `web/src/api/types.ts` | +3 fields across 3 interfaces |
| Frontend | `web/src/api/client.ts` | +1 line in `buildLogsQuery` (serialize `is_websocket`) |
| Frontend | `web/src/components/LiveRequestsPanel/RequestRow.tsx` | +WS badge ×3, tooltip, skip long-running |
| Frontend | `web/src/components/LiveRequestsPanel/utils.ts` | Skip long-running for WS |
| Frontend | `web/src/components/LogFilters.tsx` | Expand dropdown + filter badge + `hasActiveFilters` |
| Frontend | `web/src/pages/Logs.tsx` | `hasActiveFilters` + `handleClearFilters` reset |
| Frontend | `web/src/components/logs/LogsTable.tsx` | +WS badge |
| Frontend | `web/src/components/LogDetailModal.tsx` | +WS case in type display |
| Frontend | `web/src/components/TransferStats.tsx` | Generic `requestLabel`/`responseLabel` props |
| Frontend | `web/src/pages/Monitor.tsx` | Skip long-running for WS |

## Verification

1. **Backend tests**: Add `TestListLogs_FilterByIsWebSocket` in `sqlite_logs_test.go` mirroring the existing `is_sse` filter test. Extend `TestParseLogFilter_AllParams` in `handler_logs_test.go` to include `is_websocket` param parsing.
2. **Frontend tests**: Add `is_websocket: false` to all existing mock `ActiveRequest`/`RequestLog` fixtures in:
   - `web/src/hooks/useLiveRequests.test.tsx`
   - `web/src/components/LiveRequestsPanel/LiveRequestsPanel.test.tsx`
   - `web/src/pages/Monitor.test.tsx`
   - `web/src/components/LogDetailModal.test.tsx`
   - `web/src/hooks/useLogs.test.tsx`
3. **Build**: `go build ./...` + `cd web && pnpm run build`
4. **Manual check**: Start the app, trigger a WS connection (or review existing WS logs), verify:
   - Logs page shows "WS" badge on WebSocket entries
   - Filter dropdown includes "WebSocket" option and correctly filters
   - Log detail modal shows "WebSocket" type badge with "Sent"/"Received" labels
   - Monitor page shows "WS" badge on active WS connections without false long-running warnings
