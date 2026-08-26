# Codex WebSocket 兼容性执行计划

> 状态：Revised（待执行）  
> 原则：成功上游的端到端响应 Header 默认原样透传；仅剥离连接级字段和由 switch-a 负责的 WebSocket 握手字段。

## 范围

只补齐 Codex WebSocket `101` 握手元数据。既有 Probe、首帧 replay、供应商切换、`426` HTTPS fallback 和升级后重连契约不重复实现。本计划主要针对 WebSocket Probe 关闭的情况。

## Header 策略

upstream-first 时，从当前成功的 `DialExchange` 原样复制全部端到端 Header，包括未知 Header 和多值 Header。`Set-Cookie` 也默认原样透传，不列入固定过滤项（用户确定决策），且不改写 Domain/Path。

回程 `Cookie` 按来源隔离：仅转发已由 switch-a 实际投影、且绑定供应商 ID 与账号 ID（可用时）均匹配本次选择的 Cookie。未知、过期或绑定不匹配的 Cookie 直接丢弃。绑定只保存 Cookie 指纹，不保存原值、不持久化，也不扩展为通用 Cookie Jar。

网关不解释、不改写、不生成 Codex 私有 Header。仅过滤：

- `Connection` 及其声明的字段、`Keep-Alive`、`Proxy-Connection`、`Proxy-Authenticate`、`Proxy-Authorization`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade`。
- 全部 `Sec-WebSocket-*` 和 `Content-Length`；这些字段必须由下游握手重新生成或在 `101` 中省略。
- `Alt-Svc`。其他需要过滤的字段必须显式加入命名表并覆盖测试，不使用模糊类别推断。

## 时序与生命周期

- 过滤后的 Header 先暂存，只随成功的下游 `101` 原子提交；客户端握手失败时不得附带上游 Header。
- 成功连接、来源供应商和该次物理 `DialExchange` 的 Header 必须作为同一绑定传递；失败或未用于实际 relay 的连接不得提供 Header。
- Header 投影使用独立状态，不复用业务帧语义的 `ClientVisible`。
- 一旦投影供应商元数据，该上游连接即成为客户端可见边界。需要更换物理上游时，通过已升级的 WebSocket 发送携带 `status=502` 和 `WEBSOCKET_RECONNECT_REQUIRED` 的错误事件并关闭连接，由 Codex 重连并取得新连接的元数据。
- 未投影任何供应商元数据时，保留现有 replay-safe pre-visible 内部替换能力。
- 实际执行 Probe 时客户端已先收到 `101`，因此上游 Header 全部省略，不猜测、不缓存、不补发。
- 结构化日志和 session evidence 只记录投影 Header 名称、来源供应商、投影时序和省略原因，不记录 Header 值。

## 实施

1. 从现有 HTTP 响应转发中抽取共享 Header 投影核心；WebSocket 层追加握手字段、`Content-Length` 和 `Alt-Svc` 过滤，输出 Header、来源绑定和稳定省略原因。
2. 新增有界的进程内 Cookie 来源绑定；只在对应 `Set-Cookie` 随下游 `101` 提交时记录指纹，并在每次上游拨号前按供应商/账号过滤客户端 `Cookie`。
3. 让 `acceptClient` 使用仅在 `WriteHeader(101)` 时注入投影 Header 的响应边界，并保留 `Unwrap`/`Hijacker` 能力，确保无效客户端握手不泄露上游 Header。
4. 将独立的元数据投影状态加入 WebSocket 生命周期，禁止投影后透明更换物理上游。
5. Probe 路径记录 `probe_timing_unavailable`；普通缺失记录 `upstream_absent`。

## 验证

- 使用真实 WebSocket 客户端断言代理返回的 `*http.Response.Header`，不能只检查上游 capture。
- Probe 关闭或本次 bypass：端到端 Header 原样透传，包括未知 Header、重复 `Set-Cookie` 和 Codex 私有 Header；过滤字段不透传。
- 覆盖 `Connection` 动态声明字段和无效客户端握手，确认下游握手仍由 switch-a 正确生成且错误响应不泄露上游 Header。
- 覆盖同供应商凭据刷新和跨供应商尝试，确认 Header 只来自实际 relay 的成功物理连接。
- 覆盖 Cookie 回程：同供应商/账号可转发，跨供应商、跨账号、未知和过期 Cookie 均被丢弃，日志和 session evidence 不含 Cookie 原值或指纹。
- Probe 实际执行：不伪造上游 Header，model-aware 选择、首帧 replay 和业务流正常。
- 失败供应商 Header 不泄露；投影后的上游失败要求 Codex 重连，不透明换连接。
- 未投影元数据时，既有 pre-visible 内部替换正常。
- Codex 端到端覆盖同账号连续请求、不同账号重连、turn state 回传、模型目录刷新、context/token 统计和模型不一致提示。
- 保持 `426` HTTPS fallback、流式响应、错误恢复和重连行为不回归。
- 运行 `make ci`，Go 覆盖率不低于 90%。

## 完成标准

- upstream-first 时，Codex 收到成功上游提供的端到端 Header，值和多值顺序未经网关解释或改写。
- Probe 导致的缺失明确可观测。
- hop-by-hop、下游 WebSocket 握手字段和 `Content-Length` 不透传，也不记录私有 Header 值。
- 投影元数据与实际使用的物理上游连接一致。
- Cookie 不跨供应商或账号边界，且绑定状态不持久化原值。
- Codex 主业务协议、流式响应、错误恢复、重连和 HTTPS fallback 不回归。
