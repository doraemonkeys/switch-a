# Codex 跨账号对话恢复执行计划

参考：E:\Doraemon\IT\Repository\switch-a-9857820

## 背景

Codex task 一旦绑定账号，后续会要求完全相同的 upstream account authority。绑定账号配额耗尽或 suspended 时，即使另外 7 个账号健康，也会返回 503，而不会跨账号 failover。这符合身份隔离设计，不是实现错误，但相较旧版软 sticky 是非常明显的体验退化，且旧版功能运行正常，所以本计划增加可选项让用户选择回到旧版体验。


`9857820` 时期之前，HTTP/WebSocket 不同gpt账号正常切换。

Provider 切换主要是：

- 重建 `Authorization`、账号凭据；
- HTTP 保留原请求正文；
- WebSocket 保留并重放客户端帧；
- Thread、Window、Turn State、Metadata、`previous_response_id` 等继续传递。

因此，客户端重连后可以切到其他账号，同时继续原逻辑对话。

后续提交引入：

```
UpstreamAuthority = Vendor + Origin + CredentialSubject
ProtocolScope      = UpstreamAuthority + APIType
```

并通过 `RequiredAuthority` 把已有对话状态硬绑定到原账号。现在即使其他账号可能接受原状态，也会先被 switch-a 本地拦截。当前恢复计划又把跨账号直接定义成“清空状态、开启新上游对话”，这没有恢复过去良好的用户体验。用户明确表示`9857820` 时期之前的程序体验良好切换准确运行正常，除了计划文档写出的决策，其他有疑惑的可以对齐之前程序的行为。

## 目标

Codex 对话状态当前通过 `RequiredAuthority` 固定到创建状态的上游账号。原账号额度耗尽、凭据失效或没有可用路由时，其他健康账号会在请求发往上游前被排除。

本次改动增加一个全局恢复策略。启用后，switch-a 复用旧版软 sticky 和现有 Provider 选择流程：有效 sticky 优先；sticky 关闭、缺失、过期或失效时回到现有选择策略。当前 Provider 失败后，从选择器允许的其他 Codex Provider 中选择新账号，并把客户端原始对话状态原样交给新上游处理。

恢复过程不清空上下文，不删除 `previous_response_id`，也不主动创建新的逻辑对话。新上游是否接受旧状态由上游决定。

Header 清理、WebSocket subprotocol、Continuity、ClientScope 和 Provider Cookie 隔离继续常开。

`switch_account_preserve_conversation` 选项 尽量达到`9857820` 时期之前的用户故障切换体验。`preserve_conversation` 保持现有体验。

## 全局策略

新增全局设置 `conversation_recovery_policy`，对应 `ConversationRecoveryPolicy`：

| 值 | 行为 |
| --- | --- |
| `preserve_conversation` | 默认值。保持现有约束。 |
| `switch_account_preserve_conversation` | state owner 不限制选路；使用旧版 `sticky → strategy` 软粘性，并允许选择其他符合现有规则的 Codex Provider 原样延续客户端对话。 |

该设置不属于 `RoutingPolicy`。RoutingPolicy、健康状态、Provider 启用状态、模型匹配、组和现有选择策略仍共同决定候选集合；恢复策略只决定 state provenance 是否生成选路约束。

管理 API、配置导入导出和 Web 配置页使用同一个字段。配置页使用选择框表达两个互斥语义。运行时按请求读取当前值，设置变化从后续请求或 WebSocket 重连开始生效。

`client_decides` 本次不定义、不持久化，也不在界面暴露。

## 核心模型

保留以下身份关系：

```text
UpstreamAuthority = Vendor + Origin + CredentialSubject
ProtocolScope      = UpstreamAuthority + APIType
```

Continuity owner 只记录 opaque state 的来源，即 `ClientScope + ProtocolScope + RouteTargetHint`，不同时承担选路职责。

默认策略继续从 owner 派生 `RequiredAuthority` 硬约束。跨账号策略只用 owner 验证 `ClientScope` 和 `APIType`，允许同一请求携带来自多个 Authority 的合法 state；owner 不生成 `RequiredAuthority` 或 `PreferredRouteTargetID`。`RequiredAuthority` 保留其硬约束语义，不重载为软偏好。

跨账号策略下，continuity resolution 返回状态和可选 owner。已知 owner 只验证 `ClientScope` 和 `APIType`，任一不匹配仍拒绝；expired 若仍保留 owner，先执行相同验证。unknown、无 owner 的 expired，以及 continuity store unavailable 在普通代理链路仍可执行时，均按 opaque state 原样转发。默认策略继续保持现有严格错误路径。

opaque passthrough state 不生成 `RequiredAuthority`、`requiredProtocolScope`、路由偏好、Vendor context 或 continuity lease；continuity 降级不得阻止响应交付。

每个 Operation 维护仅含 `Kind + Digest + resolution` 的请求级 provenance ledger，不保存 opaque 原值。响应先按该 ledger 识别请求已有 state：已知 owner 保持原 provenance，unknown/expired/store unavailable 继续 opaque passthrough；只有请求中未出现、由当前上游新产生的 state 才 best-effort 记录到实际 Provider。

跨账号策略使用旧版 `sticky → strategy` 顺序。最后一次完成 ClientVisible 成功路径的 Provider 更新软 sticky；以 provider-scoped `reconnect_required` 结束的失败 Provider 不回写 sticky。旧 state 的 provenance 不覆盖或删除该 sticky。历史 owner 不初始化 `ProviderContinuityContext` 或 Vendor failover context；只有现有 ClientVisible continuity seed 或本次物理执行进入 ClientVisible 后，后续切换才按 Failover 执行。

沿用现有 sticky SQLite 持久化和启动恢复；sticky key 在 `IP + User + APIType + Model` 基础上加入由客户端原始 API Key 派生的 `ClientScope`，只持久化 scope digest，不保存原始 Key。

跨账号策略下，HTTP request disclosure，以及 WS dial 和客户端帧向上游的 pre-visible disclosure 所产生的 ProtocolScope、Authority、RouteTarget 约束均为 physical-attempt-local，包括 claim、adopt、response reference 和 opaque attestation。replacement 时清除这些路由约束和当前 connection generation，但不删除 durable provenance；后续 attempt 原样携带客户端 state 和 opaque attestation。默认策略继续沿用现有 operation-wide pin 行为。ClientVisible 后不再进行透明 Provider 切换。

不新增对话恢复事务、owner 迁移或全局 fence。已经在执行的并发请求互不干扰，响应提交后由最后完成的请求更新后续账号偏好。

## 候选选择与失败处理

不新增一套独立的故障分类。HTTP 和 WebSocket 继续以现有 provider failure、`errorrule`、健康状态、凭据刷新和选择器结果作为切换依据，以对齐旧版已经运行良好的行为。

请求顺序如下：

1. 按现有逻辑解析 owner。默认策略要求 owner 属于同一 ProtocolScope；跨账号策略只要求已知 owner 的 ClientScope 和 APIType 一致，允许多个 Authority，无法解析的 state 按 opaque passthrough 处理。
2. 使用现有选择器、重试、凭据刷新和 Provider 切换流程。
3. 默认策略继续用 `RequiredAuthority` 过滤候选并保持现有失败结果。
4. 跨账号策略不从 owner 生成选路约束，按软 sticky、策略、健康检查、排除列表和切换历史选择候选。
5. 选择器可以跨 Origin、CredentialSubject；Replacement 复用旧版选择语义，Failover 才应用现有 Vendor 策略。
6. 候选失败时沿用现有切换决策；可继续切换则尝试下一个，不可切换则返回现有错误。

恢复沿用现有按 Provider ID 的排除和全局 attempt budget，不新增 Authority 级排除，也不为恢复预留预算；跨账号恢复只在剩余预算内 best-effort。

客户端请求错误、协议错误和客户端断开仍走现有路径。continuity owner unknown、expired 或 store unavailable 仅在跨账号策略下降级为 opaque passthrough，其他 continuity 错误不转换成跨账号恢复。

## 状态与凭据处理

跨账号尝试遵循旧版透明转发语义：

| 数据 | 恢复行为 |
| --- | --- |
| `Authorization` 和账号凭据 | 每个物理尝试根据目标 Provider 重新构造。 |
| HTTP 请求正文 | 保持原始语义正文，不删除或重写对话字段。 |
| WebSocket 客户端帧 | 保留原始字节和顺序，只重放现有策略标记为 replay-safe 的帧。 |
| Thread、Session、Conversation、Window | 原样传递。 |
| Turn State、Turn Metadata、`previous_response_id` | 原样传递，不清空、不替换。 |
| Continuity owner | 旧 state 保留原始 provenance；新 state 记录实际产生它的目标 Provider，不迁移 owner。 |
| `X-Oai-Attestation` | 保持 opaque，不解析、不生成、不改写。 |
| Provider Cookie | 不从旧账号复制；目标账号只使用自己 `CookieAuthority` 对应的 Cookie Jar。 |

响应侧必须先查请求级 provenance ledger，不能用当前 attempt/connection 的 `ProtocolScope` 重验已存在值。B 回显请求已有的 state 时沿用入口 resolution；只有 B 新产生的 state 才 best-effort 记录为 B，存储不可用时仍转发。

如果新账号拒绝旧状态，switch-a 不静默删除状态或改成新 Thread；该结果继续交给现有故障分类和切换流程处理。

## HTTP 流程

1. `codex/http.Operation` 解析 state provenance；跨账号策略允许同一请求包含多个 Authority。
2. 跨账号策略不把 provenance 写入选择请求，按软 sticky 和现有策略进入 Provider 选择循环。
3. 每个候选重新注入认证、选择自己的 Cookie Jar，并复用原请求正文；已知旧 state acquisition 只验证 ClientScope 和 APIType，unknown、expired 或 store unavailable 时按 opaque state 转发。
4. 候选在 ClientVisible 前发生现有规则允许切换的故障时，选择循环可以尝试其他 Provider；跨账号策略按核心模型中的 attempt-local disclosure/attestation pin 语义执行，不因请求已向上一候选披露而阻止跨 Authority attempt。
5. 候选得到有效响应后正常写入客户端；新状态记录实际 Provider，响应提交后更新软 sticky。
6. 流式响应继续使用现有 ClientVisible 边界，上游错误事件不更新软 sticky。

## WebSocket 流程

- Probe 实际运行时，下游 `101` 先于 Provider 选择完成；上游握手 header 不投射给客户端，也不算 ClientVisible。
- 非 Probe 路径下，普通 `101` 不算 ClientVisible；若 `X-Codex-Turn-State` 实际投射到下游 `101`，该投射即进入 ClientVisible 并固定实际 RouteTarget。
- ClientVisible 前发生可切换故障时，销毁当前 physical-attempt-local 路由约束和 connection generation，复用现有 replay buffer、Provider 排除列表和选择循环选择其他 Codex Provider。
- `response.create` 及 opaque replay-safe 帧保持原字节重放。
- `response.append` 和 `response.inject` 继续绑定当前物理连接，不跨连接重放；出现相关连接约束错误时要求客户端重连。
- 首个有效应用帧成功写入客户端后进入 ClientVisible，并记录实际 Provider；正常成功路径更新软 sticky。
- ClientVisible 后不在同一个下游连接中切换账号。需要恢复时，先同步发布失败 health/suspension、阻止失败 Provider 回写 sticky，并建立携带失败 Provider exclusion 的一次性重连 continuity seed；随后发送 `WEBSOCKET_RECONNECT_REQUIRED`，以真实 `1012` 关闭连接。客户端重连后从新的请求边界执行恢复。

## 可观测性

沿用现有 Provider replacement/failover 结构化日志，并补充恢复策略、源/目标 Provider、switch reason、attempt 序号和 ClientVisible 状态。不得记录凭据、Cookie 或 opaque 对话值。

## 实施顺序

1. 定义 `ConversationRecoveryPolicy`、默认值和全局配置读写，接入管理 API、导入导出与 Web 选择框。
2. 拆分 provenance resolution、validation 与 routing constraint，引入请求级 provenance ledger：默认模式严格匹配 ProtocolScope；跨账号模式对可确定的 owner 验证 ClientScope/APIType，并允许 mixed-authority state；无法解析的 state 进入 opaque passthrough。
3. 调整选择入口：跨账号模式不从 owner 生成选路约束，使用 `sticky → strategy` 软粘性。
4. 接入 HTTP 和 WebSocket Operation、continuity acquisition、physical-attempt-local attestation/disclosure pin，以及现有 replay、ClientVisible、重连和 connection-bound 帧语义。
5. 补齐结构化日志和测试，覆盖 HTTP/WS A→B→C、多 Authority state、默认模式隔离、跨 ClientScope 拒绝、owner 不生成 Vendor context、sticky 按 ClientScope 隔离、重启恢复未过期且 eligible 的 sticky、off/miss/expiry/ineligible 后回到 strategy、剩余预算内恢复、unknown/expired/store unavailable opaque passthrough、B 回显旧 state 沿用入口 resolution、B 新 state 归 B、Probe/非 Probe `101` 边界、WS pre-visible disclosure 后跨账号 replacement、真实 `1012`、零延迟重连不再选择失败 Provider、并发 sticky 和失败或未提交响应不更新 sticky。
6. 运行 `make ci`。
