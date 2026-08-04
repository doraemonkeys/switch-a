# Provider Debug Capture 执行计划

## 原始需求

增加一个 debug 捕获模式：在网页启用后，在内存中保留指定 Provider 最近 N 次完整上游 exchange（默认 10），并可导出分析；不得改变现有代理行为。

## 最终决策

- 默认关闭，仅使用进程内存；每次 Start 创建新的空 session。
- 同一时刻只允许一个 active session。重复 Start 返回 `409`，不隐式替换。
- Stop 立即禁止新捕获、使旧 recorder 失效并隐藏全部旧数据；不取消代理请求。物理内存在已取消的导出/读取退出后回收，不承诺同步 GC。
- 只为选中的 Provider 保存完整 exchange；同一 gateway request 内的未选 Provider 仅保存无 payload 的 transition stub，保证重试和切换链条可解释。
- 每个 Provider 保留最近 N 条已完成记录，N 默认 10；active exchange 单独受全局上限约束。
- session retained quota 默认 256 MiB，且不得超过启动配置给出的进程级 capture ceiling。quota 是捕获器持有内存的计费上限，不等同于 Go 进程 RSS。
- 捕获 HTTP/WebSocket 语义数据，不承诺 TCP、TLS、HTTP/2 framing、header 原始顺序或 WebSocket ping/pong。

## 不得改变代理行为

捕获器是旁路观察器。启用、溢出、Stop、导出或内部失败均不得改变：

- Provider 选择、重试、故障转移、凭证刷新；
- drain 上限、超时、取消、连接复用和响应读取边界；
- SSE/WebSocket 的等待、缓冲、消息顺序及客户端可见结果；
- 健康、熔断、并发限制、粘性会话和请求日志。

关闭时热路径只做一次廉价状态判断，不构造 capture metadata、不复制 payload。开启后所有 payload 分配必须先成功预留 quota；失败只降低捕获完整性。捕获 API 不向代理返回错误，内部异常会禁用当前 recorder 并输出结构化日志。

## 核心架构

新增深模块 `internal/requestcapture`，内部由以下组件组成：

- `Manager`：session generation、Start/Stop、Provider 范围、记录索引和状态统计；
- `MemoryAccount`：进程级与 session 级原子预留、引用生命周期、query/export lease 及临时缓冲计费；
- `BlobStore`：固定上限分块、共享不可变 request blob、引用计数；
- `Sanitizer`：headers、URL、Provider 快照和错误字段脱敏；
- `Exporter`：不可变 snapshot lease 与版本化流式导出。

进程只创建一个 Manager，通过消费侧小接口分别注入 Proxy 和 Admin。Manager 接收 `Clock`、ID generator 和 logger，便于确定性测试。Proxy 使用返回 struct 的轻量 `Recorder`；Admin 只依赖 session/query/export 接口。

### Session 与并发

- 每个 recorder 绑定不可复用的 `session_id + generation + record_id`；Stop 后的旧 recorder 永远不能写入后续 session。
- Start 校验 Provider、N、quota、进程 ceiling 和 `acknowledge_raw_payload_risk=true`。
- Stop 使用 `session_id` 作为前置条件，避免旧页面停止新 session；原子摘除 active session 后取消全部 lease。摘除只释放 session 引用，底层内存在最后一个 owner 退出后才从进程 account 释放。
- completed record 完成后不可变；list/detail 返回有界值副本，export 通过 lease 读取 immutable snapshot。
- active record 导出时封存当前 chunk，后续数据写入新 chunk；snapshot 明确标记 `partial(snapshot_while_active)`，不深拷贝 payload。

## 数据模型

### GatewayTrace

一个 `GatewayTrace` 对应现有内部 gateway request ID，包含按顺序排列的 exchange 引用和无 payload transition stub。仅当请求至少命中一个选中 Provider 时保留该 trace。

为保留首次命中选中 Provider 之前的切换链，capture 开启后会暂存 active request 的 transition stub。stub 与 trace 索引同样先预留 quota，并受 `max_active_traces` 与 `max_transitions_per_trace` 约束；超限后标记 `history_truncated`。请求结束仍未命中选中 Provider 时立即释放暂存数据。

保留与淘汰单位是 exchange。淘汰造成链条缺口时，trace 暴露 `history_truncated_before/after`；不再引用任何 exchange 的 trace 和 stub 立即回收。

### ExchangeRecord

每次真实 HTTP 请求或 WebSocket dial 都生成独立 exchange，字段包括：

- session/record/gateway request ID、`exchange_index`、时间；
- Provider 白名单快照、API 类型、协议、实际目标地址；
- `provider_attempt_index`、`selection_mode`、`credential_phase`，避免用单一 reason 混合重试、切换和凭证刷新；
- 已脱敏的请求 method、URL、Host、headers、content length、trailers 和 request blob 引用；
- HTTP response，或 WebSocket handshake 与 application event transcript；
- 上游已观察字节数、写入 API 已确认的 application bytes 及终止事实；不声称数据已被远端客户端实际接收。

完整性使用正交字段：

- `source_completion`: `complete | partial`，表示网关是否读到协议终点；active snapshot 另以 `lifecycle_state=active` 表示，不提前判定最终 completion；
- `capture_completion`: `complete | overflowed | snapshot_partial`，表示捕获器是否保存了网关已观察的全部内容；
- `termination_reason`: EOF、status failover drain、credential refresh drain、client disconnect、timeout、cancel、read/write error 等稳定枚举。

Blob 始终保存原始 bytes；导出使用分块 base64，并提供原始 size 与增量 checksum，避免整块编码和 UTF-8 边界问题。

## HTTP 与 SSE 接入

1. Provider URL 和最终凭证 headers 构造完成后调用 `BeginHTTP`；因此 transport 建连失败也有完整 request exchange。
2. 当前已缓冲的 request body 视为不可变 blob，同一 gateway request 的重试和凭证刷新共享引用，不为每个 exchange 重复复制。
3. `FetchUpstream` 成功后立即记录 status、protocol、headers、content length 和 declared trailer keys。body observer 紧贴原始 `resp.Body`，位于 interceptor/drain 的数据源侧，只观察网关实际发起的读取；最终 trailer values 在 `Finish` 时采集。
4. observer 只记录实际 `Read` 返回的 bytes；不得额外读取。现有控制流必须显式、幂等调用 `Finish(outcome)`，不能从 `Read/Close` 猜测终止原因。
5. 401 凭证刷新前后的请求分别记录：首次 exchange 在原有 Drain 后结束，刷新后创建新的 `credential_phase=refreshed` exchange。
6. 普通响应、SSE、状态码故障转移、读取错误、超时和客户端断开共用 recorder，但由各自控制流提交准确 outcome。
7. 扩展 `UpstreamResponse` 保存当前会丢失的 protocol/trailers；不改变 transport 的读取策略。

## WebSocket 接入

1. 将 dial 返回重构为显式 `DialExchange`，成功和失败均保留 handshake status、headers、时间及当前流程实际读取到的失败 body；不得为捕获扩大现有 drain。
2. 每次 dial（包括凭证刷新和 Provider 切换）创建独立 exchange，并由同一 gateway request ID 串联。
3. relay 提供两阶段事件：`OnRead` 生成 message ID，随后记录 suppression decision 和实际 `Write` 结果。只有成功写给客户端才标记 `client_visible=true`。
4. message 记录方向、全局稳定序号、相对时间、text/binary、payload blob、`live|replay` 来源、`forwarded|suppressed|write_failed` disposition；replay 保留 source message ID。
5. handshake、application messages、close 和错误构成完整性边界；不捕获库内部 ping/pong 或 TCP 分片。

## 内存与保留策略

- 所有 retained 分配在创建前按统一 charge estimator 保守预留，包括 trace/stub、索引、snapshot 引用、token、lease 和固定导出缓冲。底层 blob bytes 只计费一次，但在最后一个引用释放前持续占用进程 account。
- request blob 按 gateway request 共享；response/message 使用固定大小 chunk，禁止 `bytes.Buffer` 扩容造成未计费峰值。
- record 完成时按完成序列执行每 Provider 的 N；需要更多空间时，再淘汰全局最旧且未被 snapshot pin 的 completed record。
- 没有可淘汰记录时，当前 record 停止保存 payload 并标记 `overflowed`，但继续保存有界终止 metadata。
- N 只限制每 Provider completed records；active records、active traces 和单 trace transitions 分别受启动配置硬上限约束。超过上限时跳过对应捕获并增加 dropped/truncated 计数。
- export pin 在 Stop 后仍计入进程级 account，直到取消生效并释放；新 session 也不能突破进程级 ceiling。pending export 与并发 download 具有独立硬上限，重复创建不能产生无界 lease/token。
- status 暴露 retained、pinned、releasing、record、trace、evicted、overflowed、truncated、dropped、pending export 和 active download 数量。

## 安全边界

- 脱敏在任何字段进入 Manager/BlobStore 前完成；原始凭证不得进入 record、日志或 export token。
- headers 至少覆盖 Authorization、Proxy-Authorization、Cookie、Set-Cookie、X-API-Key、API-Key、X-Goog-API-Key 及实际 credential header；比较不区分大小写。
- URL 同时清除 userinfo，并按显式敏感 key 集合脱敏 query；Provider 只保存 ID、名称、API 类型和脱敏后的实际目标地址。
- 错误优先结构化处理 `url.Error` 等已知类型，再执行认证形式和本次实际 credential value 的精确替换。
- request/response body、Prompt、模型输出、WebSocket payload 和可能包含敏感内容的上游 body 保持原文；Start API 必须显式确认风险，UI 与导出 manifest 持续提示。
- 所有 capture API 返回 `Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`；日志只记录 session/record ID、计数和稳定原因，不记录 payload。

## Admin API 与导出

除 download 外均使用现有 Admin Bearer 鉴权，资源接口为：

- `POST /admin/api/debug-capture/sessions`：Start；
- `GET /admin/api/debug-capture/status`：stopped/active 状态；
- `DELETE /admin/api/debug-capture/sessions/{session_id}`：Stop 并清空；
- `GET /admin/api/debug-capture/sessions/{session_id}/records`：使用稳定 cursor 与 snapshot watermark 分页 metadata，并报告 eviction gap；
- `GET /admin/api/debug-capture/sessions/{session_id}/records/{record_id}`：有界 metadata/body preview；
- `POST /admin/api/debug-capture/sessions/{session_id}/exports`：为单条、选中或全部记录创建 snapshot lease；
- `GET|HEAD /admin/api/debug-capture/exports/{export_id}/download?download_token=...`：流式下载与下载器探测。

详情只返回可配置上限内的 preview；完整内容只通过 export 获取。

导出采用版本化 NDJSON 事件流：`manifest`、`record`、`blob_chunk`、`record_end`、`export_end`。每行有固定大小上限；中断时已完成行仍可解析，缺少 `record_end/export_end` 可明确判断截断。Exporter 直接写固定缓冲，禁止对完整 record 使用 `json.Marshal` 或在内存中生成完整 base64。

为避免浏览器 `response.json()/blob()` 持有整个文件，创建 export 时返回短期、仅绑定该 snapshot 的高熵 download URL。download 端点不经过 Admin Bearer middleware，以 token 作为唯一 capability；HEAD 只声明流属性且不 claim，GET 串行 claim 一个流式 attempt。attempt 完成、中断或浏览器交给外置下载器后，URL 在原到期时间前仍可重试；服务端不支持 Range 分段。token 仅保存 hash，query 不写日志；Stop 或到期会取消 export 并释放 pin。

## 页面与交互

新增 `Debug Capture` 页面：

- stopped：Provider 多选、N、quota、风险确认和 Start；
- active：session ID、占用指标、按 gateway request 分组的记录、完整性标记、preview、选择导出和 Stop；
- Stop 明确提示数据立即不可访问且下载会取消；
- active 时全局导航显示状态徽标；轮询逻辑集中在页面级 hook/Context，不复制派生状态；
- payload 只按纯文本展示，完整大对象不注入 DOM。

## 执行顺序

1. 实现 Manager、MemoryAccount、BlobStore、Sanitizer、Recorder 状态机、active trace/stub 上限和 lease 生命周期，并完成并发/race 测试。
2. 重构 HTTP attempt 生命周期和 `UpstreamResponse`，接入普通响应、SSE、retry/failover、凭证刷新及全部终止路径。
3. 重构 WebSocket dial result 与 relay 两阶段事件，接入 replay、suppression、切换和 close。
4. 实现 Admin resource API、snapshot lease、NDJSON exporter 和 download token。
5. 实现页面、全局徽标、preview 与原生流式下载。
6. 完成行为等价、故障注入、race、覆盖率和完整 verify。

## 验收

1. disabled 路径无新增 payload allocation/copy；用 `AllocsPerRun` 和 benchmark 固化。
2. 开关前后代理响应、重试/切换、健康、SSE/WebSocket 顺序和客户端可见结果一致。
3. 并发追加、淘汰、active record/trace、snapshot/query lease、Stop/Start ABA 下，account 永不超过进程 ceiling；最后一个 owner 释放前不会提前扣减。
4. 网关读到的 bytes 可由导出重建；source partial、capture overflow 和 snapshot partial 相互独立且原因准确。
5. 已知认证 headers、URL userinfo/query、Provider 凭证及结构化错误中的 credential 不进入 store；raw payload 风险由 API 强制确认。
6. HTTP/SSE 覆盖 EOF、两类 drain、transport/read/write error、timeout、cancel、credential refresh 和 Provider switch。
7. WebSocket 可还原成功/失败 handshake、双向 message、replay、suppression、write result、切换和 close。
8. Stop 后旧数据不可查询，旧 recorder/旧页面不能影响新 session，代理中的请求继续完成。
9. 导出期间淘汰不会改变 snapshot；Stop/过期能释放 pin，断连文件可判断截断且 capability 可重试；download token 对无效、过期和并发消费均安全失败。
10. `go test -race` 覆盖 requestcapture/proxy/admin/server；Go 与 React 分别满足 90%/40% 覆盖率门槛。

## 当前收敛补充（优先于上文）

- 已完成且测试通过的合理实现全部保留，不因本次收敛回退 Admin、React、协议捕获、脱敏、NDJSON、token、snapshot、lease 或现有内存边界。
- 剩余阻断只包括可复现的 panic/data race/数据破坏、旧 recorder 跨 session 写入、捕获改变代理行为、凭证泄漏、资源无界增长，以及 CI 失败。
- 有界小集合允许线性扫描，不再要求为了 O(1) 句柄槽继续重构。registry 只需容量有界，不要求 backing map、token、临时 query/export 对象逐字节精确计费。
- status 只需 race-free、字段合法；Stop 只需阻止新捕获和旧 session 的新访问，不要求同步 GC、精确退款或取消已经 claim 的下载。
- 已有更强的 MemoryAccount、BlobStore、active snapshot、lease 和单次 token 语义可以保留；除非触发上述真实阻断，不再扩展或重写其所有权状态机。
- 接下来只需修复真实并发/行为问题，将捕获发布移到 health、sticky、retry 等业务提交之后，做必要的 SLOC 拆分，然后运行 race、覆盖率、lint、build 和全仓验证。
