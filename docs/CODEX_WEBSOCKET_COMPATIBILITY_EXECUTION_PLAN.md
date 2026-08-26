# Codex 状态 Header 兼容计划

## 目标

统一 Codex HTTP、WebSocket 握手和帧内状态：同一客户端、上游账号和 API 类型可延续，跨安全主体或无法验证归属时禁止复用。

状态 Header 只处理 `X-Codex-Turn-State`、`X-Codex-Turn-Metadata`、`X-Oai-Attestation`；响应引用只处理 `previous_response_id` 和 `response.inject.response_id`。值均视为 opaque，不做通用 Header 或帧框架。未经 fixture 验证的 `response.append` 不实现。Cookie 语义由独立的 [Codex Provider Cookie Jar 计划](CODEX_PROVIDER_COOKIE_JAR_EXECUTION_PLAN.md) 负责。

> 用户明确要求：本计划及后续评审不包含脱敏限制，也不得再次提出或加入脱敏设计。

## 协议依据

- [OpenAI Codex](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs) 将 Turn State 用作 per-turn sticky-routing token。
- [CLIProxyAPI PR #2333](https://github.com/router-for-me/CLIProxyAPI/pull/2333) 补齐 HTTP/WebSocket Metadata 透传。
- [CLIProxyAPI issue #4779](https://github.com/router-for-me/CLIProxyAPI/issues/4779) 说明下游先升级后无法补发上游 `101` Header。
- Codex 0.150.0 抓包验证请求帧使用 `response.create.client_metadata["x-codex-turn-metadata"]`，响应帧使用 `codex.response.metadata.headers["x-codex-turn-state"]`；上游 `101` 返回 `Set-Cookie`，但未返回 Turn State。实施前从目标版本源码和本地抓包提取最小协议 fixture，并补充同一 turn 第二次 `response.create`，确认是否携带 `client_metadata["x-codex-turn-state"]`；计划和测试不得依赖被忽略的 `.capture` 文件。

## 身份边界

- `ClientScope`：由 `internal/codexidentity` 对客户端原始 API Key 计算域分离 HMAC；第一版不读取 `installation_id`。共享 API Key 只提供租户级隔离；没有原始 API Key 时，携带本计划状态的请求关闭失败，无状态请求不受影响。
- `UpstreamAuthority`：`Vendor + normalized UpstreamOrigin + CredentialSubject`。Origin 规范化统一 `wss -> https`、`ws -> http`，小写 host，折叠默认端口，拒绝 userinfo，且不包含 path、query 或 fragment。
- `ProtocolScope`：`UpstreamAuthority + APIType`。
- `RouteTarget`：`ProviderID`，只作为同一 Authority 内的路由提示。

ChatGPT 使用实际 `ChatGPT-Account-Id` 作为 `CredentialSubject`；其他认证使用可信账号标识，无法取得时使用当前凭据的域分离 HMAC。凭据刷新只有在证明 `CredentialSubject` 不变时才保持 Authority；相同 Authority/APIType 的不同 RouteTarget 可延续状态。

凭据模型分离 `CredentialSubject`、`CredentialSession` 和 `RouteTarget`：CredentialSession 持有 CredentialSubject、secret、版本和刷新状态，刷新按 CredentialSession 协调；RouteTarget 引用 CredentialSession，多个 RouteTarget 可共享同一会话。同一账号的独立登录会话可以共享 CredentialSubject，但不共享 secret 或刷新状态。

选择前由 `internal/codexidentity.AuthorityResolver.Resolve(routeTargetSnapshot, apiType)` 从已预加载的 APIType、CredentialSession 和 AuthState 快照解析候选 Authority。ChatGPT 以 CredentialSession 的 CredentialSubject 为可信候选，`AuthState.AccountID` 只用于诊断；存在 Authority 硬约束但候选无法解析时，该候选不可选。选择后认证注入返回实际 `AppliedIdentity`，与预期 Authority 不一致时必须在任何状态发送上游前拒绝该候选。

## 字段规则

| 字段 | 请求与响应规则 | 边界 |
|---|---|---|
| `X-Codex-Turn-State` | 仅转发已验证归属的客户端值；HTTP 绑定最终响应值；WS 绑定真实 `101` 或版本化 fixture 确认的帧内值，包括确认存在时的 `response.create.client_metadata` | 不跨 ProtocolScope |
| `X-Codex-Turn-Metadata` | 原样转发，发送前原子认领 owner；不合成、不回显 | 跨 ProtocolScope 拒绝 |
| `X-Oai-Attestation` | 原样转发，不生成、不修改、不缓存、不持久化、不向下游回显；同一逻辑操作首次可能发送上游时锁定 Authority，请求结束即销毁 | 同一逻辑操作不跨 Authority |
| `previous_response_id` | 发送前验证已有 `response_ref` owner；未知、过期或冲突时拒绝 | 不跨 ProtocolScope，可在同 Authority 的新连接继续 |
| `response.inject.response_id` | 验证 `response_ref` owner 和当前连接的活动 generation | 固定 ProtocolScope 和当前上游连接 |

Header 名大小写不敏感。只启用目标版本源码和 fixture 已确认的私有状态路径；公开 [OpenAI Responses WebSocket 文档](https://developers.openai.com/api/reference/cli/resources/beta/subresources/responses) 定义 `response.inject`，但未定义 Codex 私有状态字段或 `response.append`。其他版本和私有事件必须先增加带 Codex 版本、事件方向和原始事件类型的 fixture。握手 Header 只作为兼容来源，合法帧只观察并逐字节转发。

同一请求若从 Header、metadata 或帧中带入多个状态，所有已知 owner 必须属于同一 ProtocolScope，否则拒绝。上游 `101` 与后续帧返回不同状态时不设优先级：各自在写客户端前创建 `pending`，成功写入后转为 `committed`。

## 连续性与失败语义

已验证 owner 和连接内增量会话是硬约束；sticky 和普通负载均衡始终不能覆盖 owner。约束分为 ProtocolScope、RouteTarget 和 connection generation 三层，不能用一个 pin 状态混合表达。健康检查可以拒绝硬约束范围内的候选，但不能静默越界。

| 阶段 | 行为 |
|---|---|
| 传输前，且没有 owner 状态 | 可重新选路由 |
| 即将发送 Turn State、已认领 Metadata、已披露 Attestation | 固定其要求的 Authority 或 ProtocolScope；仍可在范围内 pre-visible replacement |
| 上游数据或状态已对客户端可见 | 固定当前 RouteTarget；不得在当前下游连接切换 |
| 使用 [`previous_response_id`](https://developers.openai.com/api/reference/cli/resources/responses/methods/create) | 固定 ProtocolScope；断线后可在同 Authority 的新连接选择其他 RouteTarget |
| 使用 `response.inject.response_id` | 固定当前上游连接及其 generation |
| 上游状态尚未对客户端可见 | 丢弃该尝试；已创建的 `pending` 保留 owner 并按 TTL 清理 |
| 上游状态已经对客户端可见 | 持久绑定当前 ProtocolScope，不再跨 Authority failover |
| 新下游连接且不含旧状态或增量引用 | 可以重新选择 Authority；代理只验证不依赖旧状态，不判断历史是否完整 |

- 状态存储不可用：不请求上游，稍后重试。
- 状态未知或过期，包括功能启用前没有 owner 的 Turn State：建立新下游连接，且不使用旧状态。
- owner 冲突：拒绝请求，不删除 Metadata 后继续；外部恢复动作可与状态未知相同，内部原因必须区分。
- 已知 Authority 不可用：只重试同 Authority。
- 物理活动响应连接丢失：建立新下游连接并取消 `response.inject` 等活动响应引用；`previous_response_id` 仍可在同 ProtocolScope 使用。
- 客户端 API Key 轮换：视为新 ClientScope，不继承旧状态或 Cookie Jar。
- HTTP 状态、JSON error code 和 WS close code 在客户端兼容测试后确定，并作为正式启用前的发布门槛。
- 禁止无限 `1012` 重连。
- 错误信息必须区分“状态过期、owner 冲突、存储故障、需要新连接等”，否则安全行为正确，但用户会觉得莫名其妙。

## 实施步骤

### 1. 统一 owner 决策

`internal/codexidentity` 统一生成 ClientScope、规范化 UpstreamOrigin、解析候选 Authority，并在认证注入后校验 `AppliedIdentity`。Selector 只接收 Authority 硬约束和 RouteTarget 偏好；`internal/codexheaders` 负责纯 Header/帧决策，`internal/codexcontinuity` 负责绑定生命周期。

持久绑定记录包含类型、状态键 HMAC、ClientScope、ProtocolScope、RouteTarget 提示、claim operation ID、生命周期、密钥版本和过期时间。统一类型增加 `response_ref`；持久 owner 绑定 ProtocolScope，当前 WebSocket 会话另记录活动 response ID 对应的 connection generation。Attestation 只记录在当前逻辑操作内，不进入该存储。

- 绑定生命周期为 `pending -> committed -> expired/tombstone`。`pending` 已固定 owner 并可用于验证；网络结果不确定时保留 owner，不允许其他 Authority 认领。
- Turn State 在写客户端前创建 `pending`，写成功后转为 `committed`；未知旧值不得由首个 Provider 自动认领。
- Metadata 以完整 opaque 值的 HMAC 为键，不解析内部字段；发送上游前通过唯一约束创建 `pending`，发生或可能发生网络披露后转为 `committed`，仅可证明未传输的本地错误才可撤销。
- Response ID 首次写客户端前以 `response_ref` 创建 `pending`，写成功后转为 `committed`。`previous_response_id` 只验证持久 ProtocolScope owner；`response.inject.response_id` 还必须命中当前连接的活动 generation。未知旧 Response ID 不允许自动认领。
- HMAC 密钥必须持久配置并带版本；轮换时旧版本只验证、不签发。
- TTL 和容量按绑定类型配置。Turn State 未命中时始终拒绝首次认领；Metadata 的绑定和 tombstone 只在配置的连续性保留期内保证 owner 不被重新认领。

### 2. 接入 HTTP

凭据注入并解析实际上游 Origin 后生成 ProtocolScope。发送请求前验证 JSON 中的 `previous_response_id`；最终响应中的 Response ID 和 Turn State 只有在写客户端前才能创建 `pending`，被丢弃的尝试不得修改绑定。

### 3. 接入 WebSocket

- 握手没有 owner 硬约束时，复用现有 probe 能力接受下游并按固定大小和超时只预读取、缓冲第一条 `response.create`；从版本化 fixture 确认的固定路径取得 Metadata、Turn State 和 `previous_response_id` owner 后再选择 Provider。超时、超限或帧非法时明确关闭，缓冲帧随后逐字节重放，不引入通用消息队列。
- 检查每个 `response.create.client_metadata["x-codex-turn-metadata"]`，不能只检查首帧；按同一 owner 规则决定转发或拒绝。
- fixture 确认 `response.create.client_metadata["x-codex-turn-state"]` 时，检查每次出现的值并应用与 Header 相同的 owner 规则。
- 检查每个 `response.create.previous_response_id`；检查每个 `response.inject.response_id` 的 ProtocolScope 和当前 connection generation。`response.append` 不实现。
- 上游帧首次暴露 Response ID 前创建 `response_ref pending`，写成功后提交，并在响应活动期间登记当前 connection generation。
- 非 probe：先连接上游；`101` 含真实 Turn State 时先创建 `pending`，投射到下游并在实际提交后转为 `committed`，否则等待已验证的帧内值。
- probe：不回显或合成 Turn State；帧内真实值在写客户端前创建 `pending`，成功写入后转为 `committed`。没有 fixture 验证的帧内路径时，不签发或认领新 Turn State；已有已验证状态只能在其 Authority 硬约束内使用。
- 有握手 owner 时按 owner 选择 ProtocolScope；只有在握手和预读取帧都没有 owner 时才按普通策略选择。Metadata 未绑定值在首次发送上游前原子认领当前 ProtocolScope，冲突值在选路前拒绝。
- Metadata 或 Attestation 首次可能发送上游后只锁定对应 ProtocolScope 或 Authority；在客户端尚不可见且没有活动 response 时，仍允许约束范围内的 RouteTarget replacement。上游数据或状态对客户端可见后固定 RouteTarget；`previous_response_id` 只固定 ProtocolScope，`response.inject` 固定当前上游连接及 generation。

## 发布与验证

状态连续性和会话身份 Header 边界使用同一发布开关。认证 Header 清理和 WebSocket subprotocol 可独立启用。

- 覆盖 AuthorityResolver 与 AppliedIdentity 一致/冲突、并发 claim、同/跨 Scope、HTTP 最终响应和 discarded attempt。
- 从目标 Codex 版本源码和抓包提取并提交最小真实帧 fixture，覆盖同一 turn 第二次 `response.create`、仅 Header、仅帧、两者冲突、无已验证帧路径、帧写失败和重启恢复；测试不得读取 `.capture`。
- 覆盖跨客户端/账号/Origin/APIType、API Key 轮换、probe/非 probe、`previous_response_id` 同 Scope 跨连接和 RouteTarget、`response.inject` connection generation、断线重连、Authority 不可用及完整重放。
- 新增 CredentialSession 持久模型并回填现有 ProviderCredential；RouteTarget 改为引用 CredentialSession，移除账号独占校验和替换语义，覆盖共享会话刷新协调、独立会话隔离及同账号多个 RouteTarget 的状态延续。
- 记录结构化决策日志，至少覆盖 claim、owner 冲突、未知或过期、存储故障、pin 拒绝和恢复动作；使用稳定 operation/session ID。
- 最终运行 `make ci`，Go 覆盖率不低于 90%。

## 完成标准

同一 ClientScope 和 ProtocolScope 的状态及 `previous_response_id` 可跨 HTTP/WS 延续；首帧 owner 在选路前生效；pre-visible replacement 可在硬约束范围内进行，客户端可见后固定 RouteTarget，活动 `response.inject` 固定连接 generation；任何越界、归属冲突或无法安全续接的情况都明确失败。
