# Transport 可观测性计划（SSE + WebSocket）

## 目标

为 SSE 与 WebSocket 建立统一的 transport 诊断事实，让以下场景留下稳定、结构化的证据：

- SSE：`ErrSSEIdleTimeout`、上游读取失败、客户端收到部分响应后的流中断
- WebSocket：`transport_error` + `completion_state = completed` + `error = ""` + `session_evidence_json = null` 这类只有粗粒度分类的终止

两种协议共用同一份 `transport` evidence contract 与同一个 diagnostic 派生函数；后端实现链路保持独立。

## 设计决策

1. **不改变运行语义**：不修改 `termination_reason` / `service_outcome` / circuit breaker / close propagation / `Err` 语义；不为补日志回填主错误通道。**TerminalCause 继承策略也不动**——synthetic final session 该继承什么仍按原规则；diagnostic 派生不读 cause，不会被继承的 cause 污染。
2. **Diagnostic 是 evidence 层的派生概念，不是 result 的固有属性**：
   - `forwardResult` / `WebSocketResult` / `webSocketRelayOutcome` **不挂** `TransportDiagnostic` 字段。
   - 它们只承载真实运行时观测：`err`、`closeError *websocket.CloseError`、`firstByteVisible`、`failurePeer` 等。
   - 单点派生函数 `deriveTransportDiagnostic(obs) → *transportDiagnostic` 在 evidence builder 内调用，纯函数、可独立单测。
   - `reduceOrderedWebSocketRelayResults` 保持纯净，不承担 diagnostic 派生。
3. **request-level / transport-level / cause 三轴正交**：`transport.kind` 不替代 `TerminalCause`；`transport.signal` 不替代 `termination_reason`。三个轴互不污染。
4. **纯客户端主动断开不产 transport evidence**：派生函数对"纯 cancel 无 transport 事实"路径返回 nil。WS 的 `CloseNow` / close-without-status 即使 `Err == nil` 也允许产 diagnostic（observation 中含真实 close 信号），但不把 `client_disconnect` 升级为 `transport_error`。
5. **ctx cancel 与 transport 失败正交、各管各的轴**：`clientCanceled`（request-level）仍由 `ctx.Err() != nil` 驱动，**不变**；transport diagnostic（evidence-level）只看是否存在真实 transport 信号——
   - 纯 ctx cancel（仅 `obs.ctxErr != nil`、无 `err` / `closeError`）→ nil
   - ctx cancel + 真实信号（如 ctx deadline 与 upstream `ErrSSEIdleTimeout` 并发）→ 仍出 diagnostic
   - 这条规则保证 root cause 不会被 ctx 短路吞掉，并与决策 3（三轴正交）一致。
6. **不保留旧 schema 兼容，但用版本号解除部署耦合**：evidence JSON 顶层加 `"v": 2`。前端按版本路由 renderer——`v === 2` 走新 renderer、缺失或 `v === 1` 走旧 renderer（保留用于渲染历史数据）。后端只产 v2。这样前后端可任意顺序上线，无空白期。
7. **本阶段不新增 DB 筛选列**：诊断只进 `session_evidence_json` / `attempt_evidence_json`。

## 共享 transport 契约

Evidence 中新增统一结构（路径：`session_evidence_json.transport` 与 `attempt_evidence_json.transport`）。顶层 evidence JSON 含 `"v": 2`。

| 字段 | 含义 | 作用域 |
| --- | --- | --- |
| `source` | `upstream` / `client` | 两协议 |
| `stage` | `pre_connection_visible` / `pre_payload_visible` / `post_payload_visible` | 两协议 |
| `kind` | root cause 分类，前端 summary 用：`timeout` / `disconnect` / `protocol_error` / `local_error` | 两协议 |
| `signal` | 协议层观测信号，详情页用 | 两协议 |
| `raw_error_snippet` | 诊断原文片段，不等价于主 `error` 通道 | 两协议 |
| `close_code` | Go 类型 `*int`、JSON `omitempty`：派生函数从 observation.closeError（`*websocket.CloseError`）提取并取 `*int` 包装，**仅真实观测时出现**；presence 由指针是否为 nil 决定，**不依赖零值省略**（避免与未来 close code 0 等边界歧义）。与 `WebSocketResult.CloseCode`（含合成值）无关 | WS 专用 |
| `close_reason_snippet` | WS close reason 片段 | WS 专用 |

`signal` 取值：

- SSE：`sse_idle_timeout` / `upstream_read_error` / `client_write_error` / `unknown_transport`
- WS：`eof` / `unexpected_eof` / `close_without_status` / `close_error` / `timeout` / `canceled` / `unknown_transport`

`kind` ← `signal` 映射（派生函数维护）：

- `timeout` ← `sse_idle_timeout` / `timeout`
- `disconnect` ← `eof` / `unexpected_eof` / `close_without_status` / `close_error`
- `protocol_error` ← `upstream_read_error` / `client_write_error`
- `local_error` ← `canceled` / `unknown_transport`

`stage` 三态边界：

- **SSE**：
  - `pre_connection_visible`：HTTP header 尚未提交给客户端
  - `pre_payload_visible`：header 已提交、首个 body byte 尚未写出
  - `post_payload_visible`：首个 body byte 已写出（`firstWriteResponseWriter.written == true`）
- **WS**：
  - `pre_connection_visible`：dial 阶段，无 HTTP 响应（dial 拿到 HTTP 响应时只产 `upstream_handshake` evidence，不产 transport diagnostic）
  - `pre_payload_visible`：upgrade 完成、relay 尚未投递任何 frame
  - `post_payload_visible`：已有 frame 投递

## 后端改造

### Observation 层（result 类型新增字段）

仅承载真实运行时事实，不承载派生结论：

- `forwardResult`：新增 `firstByteVisible bool`（复用 `firstWriteResponseWriter.written`，声明在 `handler.go:60-69`、`Write()` 内赋值在 `handler.go:76-81`、`handler_forward.go:173` 已读，仅需向 result 透传）。observation 还包含 `err` / `ctxErr` / `headerCommitted` / `isStatusFailover` 这些已有但未集中的运行时信号。
- `WebSocketResult`：新增子结构 `TransportObservation`，承载 `closeError *websocket.CloseError`（仅当真实观测到 close error 时赋值）与 `failurePeer webSocketPeer`。**子结构边界关键作用**：`applyLastAttemptToSuppressedPayload` 等继承路径在拷贝 result 时**显式将 `TransportObservation` 清零**（强制 synthetic final session 重新构造自己的 observation，否则就是被继承进来的 attempt 数据），不靠"约定不读"。`Clone()` 复制 `TransportObservation`（`CloseError` 一旦观测即不可变，复制指针即可）。
- **per-peer 传递链**：`webSocketRelayResult` 也新增对应字段（至少 `closeError`、`failurePeer`），`reduceOrderedWebSocketRelayResults` 选 candidate 后才能把 close error 透传到 `WebSocketResult.TransportObservation`。漏了这一环 candidate 选出来也是空的。
- `WebSocketResult.CloseCode`（含 `reduceOrderedWebSocketRelayResults` 在 `isUnexpectedPeerDisconnect` 分支合成的 `StatusNoStatusRcvd`）保持原语义、只供 close propagation 使用，**派生函数不读它**。

### 派生层（单点纯函数）

新增 `internal/proxy/transport_diagnostic.go`：

```
deriveTransportDiagnostic(obs transportObservation) *transportDiagnostic
```

- 对 SSE / WS 各定义一个 observation 结构，分别承载本协议必要字段；二者经同一派生入口出 diagnostic。
- 函数纯：无 IO、无 ctx 副作用，仅根据 obs 决定返回 diagnostic 还是 nil。
- 短路规则（按顺序）：
  - 无任何 transport 信号（既无 `err` 又无 `closeError`）→ nil
  - `obs.isStatusFailover == true` → nil（状态码 failover 是状态分类事实，不是 transport 失败）
  - `obs.isSuppressedSyntheticFinal == true` → nil（synthetic final session 不复用被替换 attempt 的 observation，详见 WS 链路接入）
  - `obs.ctxErr != nil` 且 **无** `err` / `closeError`（纯 client cancel）→ nil
  - **否则**（即便 `ctxErr != nil`）只要存在真实 `err` 或 `closeError` 信号 → 出 diagnostic

### SSE 链路接入

路径：`internal/proxy/transport.go`、`handler.go`、`handler_forward.go`、`handler_execute_proxy.go`、`handler_util.go`、`request_attempt.go`，新增 `internal/proxy/transport_diagnostic.go`、`internal/proxy/non_websocket_assessment_evidence.go`。

- `forwardResult` 仅新增 `firstByteVisible bool`，**不挂 diagnostic 字段**。
- `handleWriteError` 的判定保持不变——`clientCanceled` 仍由 `ctx.Err() != nil` 驱动（`handler.go:632`）；health / success（`markFailure` / `markSuccess`）后续可改为基于派生 diagnostic.kind 而非错误文本链式判断。
- `nonWebSocketRuntimeFacts`（`handler_util.go:240`）扩展承载 SSE observation 字段（err、firstByteVisible、ctxErr、headerCommitted、isStatusFailover）；`nonWebSocketAssessment` 透出。
- `logRequest` 当前 12 个标量参数（`handler_util.go:146-159`）。改为接受 assessment 聚合体，避免后续再膨胀。最终 `session_evidence_json` 由 evidence builder 调派生函数产出，不再从 `logRequest` 末端反推。
- **SSE attempt evidence 写入路径**：当前 `recordAttempt`（`handler.go:422-447`）不写 `AttemptEvidenceJSON`（仅 WS 在 `websocket_session_models.go:195` 写）。新增独立小路径——caller 在调 `recordAttempt` 后显式调 `attachSSEAttemptEvidence(attemptID, obs)`，与 WS 的 `buildWebSocketAttemptEvidence` 对称。**不把 evidence 派生塞进 `recordAttempt`**，避免 HTTP attempt 薄抽象被污染。

### WebSocket 链路接入

路径：`internal/proxy/websocket_relay_result.go`、`websocket_relay_state.go`、`websocket_relay_outcome.go`、`websocket.go`、`websocket_session_result.go`、`handler_websocket.go`。

- `WebSocketResult.TransportObservation` 子结构承载 `closeError` + `failurePeer`；`Clone()`（`websocket.go:178`）补上对应复制。
- `webSocketRelayResult`（per-peer）也新增 `closeError` + `failurePeer`，否则 `reduceOrderedWebSocketRelayResults` 拿不到 close error。
- `webSocketRelayOutcome` **不**新增 diagnostic 字段；`reduceOrderedWebSocketRelayResults`（`websocket_relay_result.go:81`）逻辑不动，仅在选出 candidate 后把 `candidate.err` / `candidate.closeError` / `candidate.failurePeer` 写入 `WebSocketResult.TransportObservation`。`newSinglePeerRelaySessionResult`（`websocket_relay_state.go:176`）走同一 reduction 路径自动覆盖。
- evidence builder 调 `deriveTransportDiagnostic(obs)`，obs 由 `WebSocketResult.TransportObservation` 构造。
- `newSuppressedPreVisibleRelayResult`（`websocket_relay_outcome.go:39`）仍走 `TerminalUpstreamSemanticError`，observation 中无 transport 信号，派生返回 nil（与状态码 failover 同侧）。
- **TerminalCause 继承策略不动**——`applyLastAttemptToSuppressedPayload`（`websocket_session_result.go:132-163`）不改 cause 拷贝逻辑，但**必须显式清零拷贝后 result 的 `TransportObservation`**（同时 obs 上的 `isSuppressedSyntheticFinal = true` 由 caller 在派生入口设置，作为双保险）。synthetic final session 的 diagnostic 因此只能由其自身 observation 产生；继承的 cause 不污染 diagnostic（派生函数本就不读 cause）。

### Evidence 生成统一

路径：`internal/proxy/websocket_assessment_evidence.go`、`websocket_assessment.go`、`websocket_session_models.go`、新增的 SSE evidence builder。

- 替换 `webSocketTransportEvidence`（`websocket_assessment_evidence.go:51-57`）为新结构（`source` / `stage` / `kind` / `signal` / `close_code` / `close_reason_snippet` / `raw_error_snippet`），删除 `message_snippet` / `is_timeout` / `is_client_cancel`。
- evidence JSON 顶层（session 与 attempt）加 `"v": 2` 常量字段。
- session evidence 与 attempt evidence 共用同一 `deriveTransportDiagnostic`；clean close 派生 nil 也不产生 evidence。
- **`attempt.Result == nil` 兜底**：handshake 前失败等路径上 `WebSocketResult` 可能不存在。`buildWebSocketAttemptEvidence` 在此情形下用 `attempt.terminalErr()` 合成最小 observation（`err = attempt.terminalErr()`、`failurePeer = webSocketPeerUpstream`、`closeError = nil`、`stage = pre_connection_visible`），交派生函数处理。漏写这一支会让 handshake 失败彻底无 evidence。
- `visible_session` attempt 在 teardown 为 transport 异常时仍携带 attempt evidence；final session evidence 仅含其自身 observation 派生的 diagnostic，不混入被替换 attempt 的 observation。
- **同步 `marshalWebSocketEvidence` 的裁剪顺序**（`websocket_assessment_evidence.go:142-175`）：旧顺序 `RawPayloadSnippet → RawErrorSnippet → HandshakeBodySnippet → GatewayMessageSnippet → UpstreamEventMessageSnippet → TransportMessageSnippet`。schema 切换后 `MessageSnippet` 不存在，需移除；新增 `Transport.CloseReasonSnippet` 应在 `RawErrorSnippet` 之前裁（close reason 通常更易复原）。新顺序：`RawPayloadSnippet → CloseReasonSnippet → RawErrorSnippet → HandshakeBodySnippet → GatewayTerminalMessageSnippet → UpstreamEventMessageSnippet`。

## 前端改造

路径：`web/src/api/types.ts`、`web/src/components/logs/evidence-utils.ts`、`diagnostics.ts`、`evidence.tsx`、`LogsTable.tsx`、`web/src/components/LogDetailModal.tsx`、`RequestAttemptTimeline.tsx`、`ProviderDetailDrawer.tsx`、`web/src/pages/DashboardSections.tsx`。

- **按 `evidence.v` 路由 renderer**：`v === 2` 走新 schema、缺失或 `v === 1` 走旧 schema（旧 renderer 保留用于渲染历史数据，不删）。这样前端可独立于后端任意顺序上线。
- 新 renderer `RequestEvidenceTransportV2` 解析 `source` / `stage`（三态）/ `kind` / `signal` / `close_code` / `close_reason_snippet` / `raw_error_snippet`。
- 统一 summary helper：优先显示顶层 `error`；为空或泛化时按 `kind` + `signal` + `stage` 格式化（如 `upstream timeout (sse_idle_timeout) before payload visible`、`upstream disconnect (close_without_status) after payload visible`）。
- `LogsTable` / `LogDetailModal` / `ProviderDetailDrawer` / `DashboardSections` / `RequestAttemptTimeline` 共用该 helper。
- 客户端 `close_without_status` 不作为列表摘要首选项，详情页与 attempt timeline 仍展示完整 transport evidence。
- 不根据 `termination_reason = transport_error` 反推文案。

## 不做事项

- 不修改 `termination_reason` 归类、close propagation、`Err` / `responseCommitted` 语义。
- **不修改 TerminalCause 继承策略**——`applyLastAttemptToSuppressedPayload` 不动。
- 不把客户端主动断开升级成 `transport_error`。
- 不为纯 client cancel 产 transport evidence。
- 不引入 `transport.status_code`。
- **不为状态码 failover 产 diagnostic**：`failoverForwardResponse`（`handler_forward.go:190-227`）合成的 `"upstream returned status %d"` 通过 `obs.isStatusFailover == true` 显式 bypass。
- **不在 result 类型上挂 `TransportDiagnostic` 字段**——diagnostic 是 evidence 层派生，result 只持原始 observation。
- **不引入第二个 close-code 字段**（如 `ObservedCloseCode`）——派生函数从 `closeError *websocket.CloseError` 现场提取，避免在 result 上留两个语义不同的 close-code 永久陷阱。
- **不在 `recordAttempt` 内派生 evidence**——SSE 走独立 caller-pass 小路径与 WS 对称。
- 不为旧 `message_snippet` / `is_timeout` / `is_client_cancel` 数据保留兼容分支（v1 数据由 v1 renderer 渲染，不在 v2 renderer 内做向后兼容）。
- 不要求 SSE 与 WS 在同一次改动中交付。

## 落地顺序

引入 `"v": 2` 后，前后端解耦；五步可大量并行：

1. **共享契约定稿**（本文档 + Go struct 定义 + JSON tag + 派生函数签名）。
2. **派生函数 + observation 结构**（`transport_diagnostic.go`）：纯函数与 observation 类型，独立 PR、独立单测。后续两个后端各自接入。✅ Done
3. **WS 后端**：`WebSocketResult` 加 `closeError` / `failurePeer`、`Clone()` 补复制、`reduceOrderedWebSocketRelayResults` 写入 observation 字段、evidence builder 接入派生函数、写 `"v": 2`、replace `webSocketTransportEvidence` 字段、同步 trim 顺序。**与 #4 互不依赖**。✅ Done
4. **SSE 后端**：`forwardResult.firstByteVisible` + observation 透传 + assessment / `logRequest` 接入聚合体 + `attachSSEAttemptEvidence` 独立写入路径 + 写 `"v": 2`。**与 #3 互不依赖**。✅ Done
5. **前端 v2 renderer**：可与 #3 / #4 任意顺序部署——v2 数据未到时按 v1 渲染，v2 数据到了按 v2 渲染。✅ Done

## 测试计划

### Go

- **派生函数单测**（最重要，纯函数易测）：
  - 短路：无信号 → nil；`isStatusFailover=true` → nil；`isSuppressedSyntheticFinal=true` → nil；纯 ctx cancel（仅 `ctxErr` 无 `err`/`closeError`）→ nil。
  - **ctx + 真实信号并发**：`ctxErr != nil` 且 `err = ErrSSEIdleTimeout` → 仍出 diagnostic（`kind=timeout`/`signal=sse_idle_timeout`），不被 ctx 短路吞掉；`ctxErr != nil` 且 `closeError != nil` → 仍出 diagnostic。
  - SSE：`ErrSSEIdleTimeout` → `kind=timeout` / `signal=sse_idle_timeout`；`firstByteVisible=true` → `stage=post_payload_visible`；`firstByteVisible=false` 且 `headerCommitted=true` → `pre_payload_visible`；`headerCommitted=false` → `pre_connection_visible`；上游读取失败 → `signal=upstream_read_error` / `kind=protocol_error`；非 cancel 客户端写失败 → `signal=client_write_error`。
  - WS：`closeError` 含真实 code → `signal=close_error` 且 `close_code` 出现（`*int` 非 nil，包含 1000 等正常 code）；`isUnexpectedPeerDisconnect` 路径（`closeError == nil` 但 `WebSocketResult.CloseCode == StatusNoStatusRcvd`）→ `signal=close_without_status`、`close_code` **不**出现（指针为 nil）；`eof` / `unexpected_eof` / `timeout` / `canceled` 各自映射；dial 失败无 HTTP 响应 → `stage=pre_connection_visible`；upgrade 后 relay 失败按 frame 是否投递分 `pre_payload_visible` / `post_payload_visible`。
- **集成路径**：`forwardResult` → `retryState` → `logRequest` 链路 observation 不丢失；`attachSSEAttemptEvidence` 能为 SSE 产 `attempt_evidence_json`；WS per-peer `webSocketRelayResult` 携带 `closeError` 经 reduction 写入 `WebSocketResult.TransportObservation`；`Clone()` 保留 `TransportObservation`；`attempt.Result == nil` 时 `buildWebSocketAttemptEvidence` 用 `attempt.terminalErr()` 兜底产生 evidence；`newSuppressedPreVisibleRelayResult` 路径派生 nil。
- **synthetic final 防护**（结构性测试，不能只靠口头约定）：构造 last attempt 含 `TransportObservation.closeError != nil` 的场景 → `applyLastAttemptToSuppressedPayload` 后 final session 的 `TransportObservation` 必须清零；其 `buildWebSocketSessionEvidence` 派生结果必须为 nil；TerminalCause 继承不变。
- **运行语义无回归**：`termination_reason` / `service_outcome` / circuit breaker / close propagation / TerminalCause 继承策略不变。

### React

- v1 数据用旧 renderer 仍能渲染（不回归历史日志显示）。
- v2 数据用新 renderer 解析三态 stage、`kind`、`signal`。
- `error` 为空时 `LogsTable` / `LogDetailModal` / `ProviderDetailDrawer` / `DashboardSections` 共用同一 transport summary helper。
- `RequestAttemptTimeline` 能展示 SSE 与 WS `visible_session` attempt 的 transport evidence。

## 验收标准

1. **SSE idle timeout**：`termination_reason = transport_error`、`transport.kind = timeout`、`signal = sse_idle_timeout`、`source = upstream`；`stage` 取决于 observation——`firstByteVisible=true` → `post_payload_visible`，否则按 `headerCommitted` 分 `pre_payload_visible` / `pre_connection_visible`。watchdog 从 body open 起跑（`transport.go:517` 调 `newIdleWatchdog`，定时器立刻起跑早于首次 `reader.Read()`），故首字节前即可触发。`error` 为空时 UI 仍显示 `upstream SSE idle timeout (before payload visible)`。
2. **SSE 上游读取失败**：`attempt_evidence_json.transport.signal = upstream_read_error` / `kind = protocol_error` / `source = upstream`；`request_attempts.error` 保留 `UpstreamReadError.Error()` wrapper 文本（`"upstream read error: <inner>"`，`errors.go:32-34`），不强制 unwrap。
3. **SSE stage 三态边界**：header 未提交 → `pre_connection_visible`；header 已提交未发首字节 → `pre_payload_visible`；首字节后 → `post_payload_visible`。
4. **WS completed + transport_error**：`source = upstream`、`stage = post_payload_visible`、`signal ∈ {eof, unexpected_eof, close_without_status, close_error}`；`close_code` 仅当 `WebSocketResult.closeError != nil` 时出现，**不反映 `WebSocketResult.CloseCode` 的合成值**；`error` 为空时 UI 显示格式化摘要。
5. **WS pre-connection 边界**：HTTP 拒绝只写 `upstream_handshake` evidence；dial 超时 / 取消 / 无 HTTP 响应的网络失败 → `stage = pre_connection_visible`。
6. **WS attempt/session 边界**：final session evidence 仅含其自身 observation 派生的 diagnostic，不继承被替换 attempt 的 observation；`visible_session` attempt 在 transport teardown 下可见 attempt evidence；TerminalCause 继承规则不变（说明：被继承的 cause 不会污染 diagnostic，因为派生函数不读 cause）。
7. **客户端主动断开**：SSE 纯 cancel → `service_outcome = abandoned_by_client` 且 `session_evidence_json` 为空；WS `client_disconnect` 允许带 `signal = close_without_status` 但 `termination_reason` 不变。
8. **Contract 对齐**：SSE 与 WS 共用同一 `transport.{...}` 结构与三态 `stage`；evidence JSON 含 `"v": 2`；无协议专属平铺字段；无 `transport.status_code`。
9. **运行语义无回归**：close propagation / `Err` / `termination_reason` 归类 / TerminalCause 继承策略不变。
10. **Schema 共存**：v1 历史数据由前端 v1 renderer 正常渲染；v2 数据由 v2 renderer 渲染；前后端部署顺序无强制依赖。
11. **派生层独立**：`forwardResult` / `WebSocketResult` / `webSocketRelayOutcome` 不含 `TransportDiagnostic` 字段；`reduceOrderedWebSocketRelayResults` 不含 evidence 派生逻辑；diagnostic 仅在 evidence builder 内通过 `deriveTransportDiagnostic` 单点派生。
12. **synthetic final 结构性防护**：`WebSocketResult.TransportObservation` 在 `applyLastAttemptToSuppressedPayload` 后必须清零，且派生入口 `obs.isSuppressedSyntheticFinal=true` 双保险——靠测试覆盖，不靠口头约定。
13. **`close_code` presence 语义**：Go 类型 `*int`、JSON 由 nil 决定是否出现；真实 code 0（理论边界）也能正常出现，永不与"未观测"混淆。
14. **ctx + 真实信号并发不丢 evidence**：`ctx.Err() != nil` 与 `ErrSSEIdleTimeout` / WS `closeError` 并发时，diagnostic 仍生成；`clientCanceled` 仍按 ctx 驱动——两轴独立。

## 状态

**Completed** — 2026-04-22

## 补丁

- **2026-04-22 post-review fix（Codex findings）**：
  - SSE `raw_error_snippet` 未脱敏——抽取共享 `sanitizeEvidenceSnippet`（`internal/proxy/evidence_sanitize.go`），SSE 与 WS 共用；`marshalNonWebSocketEvidence` 在序列化前调用，关闭 `Authorization` / `Bearer` / `api_key` 泄漏渠道。
  - WS `source` 固定写死 `upstream`——`wsObservation` 新增 `failurePeer`，由 `buildWebSocketTransportDiagnostic` 从 `WebSocketResult.TransportObservation.FailurePeer` 透传；`classifyWSSignal` 对四个 disconnect 信号（`close_error` / `eof` / `unexpected_eof` / `close_without_status`）按 peer 分派 source；`timeout` / `canceled` / `unknown_transport` source 语义不变。`close_code` presence 与 peer 正交（`WS_ClientCloseErrorKeepsCloseCode` 锁定）。
