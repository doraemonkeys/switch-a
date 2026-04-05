# 无首包失败不算 Failover 实现计划

## Status
Completed

## 目标
- Provider 已选中，但在任何客户端可见的上游数据出现前失败，这类跨 Provider 切换定义为 `replacement`，不是 `failover`。
- `failover_scope` 和 `accept_failover` 只约束真正的 `failover`，不约束 `replacement`。
- 这里的 `failover` 不只包含“同一请求里 post-visible 后继续切换”，也包含“新请求 best-effort 承接了之前已对客户端可见的上游连续性后，再切到其他 Provider”。

## 语义边界
- HTTP / JSON / SSE：以网关首次成功向客户端提交上游响应为界（首次 `WriteHeader` 或首次 `Write`）。此前失败是 `replacement`；此后禁止跨 Provider 静默切换。不要复用当前 `headersWritten` 作为可见边界，需要显式的 `client_visible` / `response_committed` 状态。
- WebSocket：以 `ClientVisible` 为界。仅握手成功不算 `failover`，仍属于未可见阶段。
- “选中了 Provider” 不是 failover 起点；“客户端已经看到上游服务”才是。
- `replacement` 的作用域限于当前 request execution chain：只有当前请求尚未暴露任何上游结果时，才允许透明替换 Provider。
- `failover` 的判定基于“这次跨 Provider 切换是否离开了一个已承接的可见连续性来源”，而不是“是否还是同一个 inbound request”。
- `visible continuity seed` 由独立的 `VisibleContinuitySeedStore` 持有，不挂在 `ActiveRequestRegistry` 或 `ProviderContinuityContext` 上；它是最近一次 `post-visible` 连续性中断留下的短时共享线索，至少包含稳定 `SeedID`、`ContinuityKey`、`OriginProviderID`、`OriginVendor`、`ObservedAt`、vendor isolation 所需上下文。
- 在没有显式会话标识、没有 continuity token、也不扫描历史消息的前提下，跨请求 continuity 只做 `best-effort heuristic`：上一轮请求必须 `post-visible` 后异常结束；窗口固定为 `VisibleContinuitySeedTTL = 5s`（命名 const，当前阶段不做 runtime config）；新请求在补齐 `ContinuityKey` 所需前置条件后，于 5 秒内命中相同 `ContinuityKey` 的 seed；且该新请求首个 selection 先通过 sticky / active continuity 粘回原 `OriginProviderID`。只有满足这些条件，才视为“新请求承接了已可见连续性”。
- seed 采用 one-shot consume，但共享 store 只暴露只读 `continuity candidate` 快照，不在 key 命中时直接 materialize `ProviderContinuityContext`。消费发生在“首个 selection 实际 re-entry 到 `OriginProviderID`”之后，并且必须对命中的 `SeedID` 做原子 compare-and-consume；只有 compare-and-consume 成功的请求才能 attach continuity。消费后会把 seed 快照复制到 request-local `ProviderContinuityContext`，共享 seed 删除，后续 leave-origin 切换仍按 `failover` 判定。
- 同一 `ContinuityKey` 任一时刻最多只保留一条未消费 seed；若 TTL 窗口内出现多次 `post-visible` 异常中断，则以 `ObservedAt` 最新的 seed 覆盖旧 seed。
- 如果 seed 命中但 origin provider 因 health / auth / routing / concurrency 无法完成 re-entry，或 compare-and-consume 败给并发请求，则当前请求降级回普通 `initial` / `replacement`，不能因为“本来想承接 continuity”而直接进入 `failover`。

## 现状问题
- 当前 `FailoverContext` 在首个 Provider 选中后立即创建，导致后续任何 Provider 切换都会被当成 failover。
- selector 只要看到 `FailoverContext` 就会执行 `failover_scope` / `accept_failover` 校验。
- 结果是“无首包失败”被错误套进 vendor failover 隔离规则。
- 反过来，当前模型也无法表达“新请求先粘回原 Provider 承接已可见连续性，随后离开该 Provider 时应进入 failover”的场景，因为跨请求 continuity 目前只有 affinity，没有 continuity seed 语义。

## 重构方案
### 1. 领域模型
- 不再用单个 `FailoverContext` 同时承载“切换历史”和“failover 语义”。
- 显式拆成两部分：
  - `SwitchMode: initial | replacement | failover`，表示当前这次 provider selection 的进入语义
  - `ProviderSwitchHistory`，承载当前请求内的 `OriginProviderID`、`AttemptChain`、`ProviderSwitchCount`
  - `ProviderContinuityContext`，承载当前请求已经成功承接下来的可见连续性来源，如 `VisibleOriginProviderID`、`ContaminatedVendors`、`StrictestScope`、`ObservedAt`；它只能在首个 selection 成功 re-entry origin provider 后 materialize
  - `VisibleContinuitySeedCandidate`，承载请求入口命中的 seed 快照，如 `SeedID`、`OriginProviderID`、`OriginVendor`、`ObservedAt`、`Age`；它只表示“可能承接 continuity”，不是 failover 语义
  - `VisibleContinuitySeedStore`，承载跨请求共享的短时 continuity seed，负责写入、查找、原子 compare-and-consume 与过期；它不回答当前请求内切换历史，也不直接参与 eligibility 判定
- `ProviderSwitchHistory` 只回答“当前请求里已经切过哪些 Provider”；`ProviderContinuityContext` 只回答“这次 selection 是否在承接之前已对客户端可见的上游连续性”。
- 仅 `Mode=failover` 才读取 / 更新 `ProviderContinuityContext` 并执行 vendor isolation；`replacement` 只复用 `ProviderSwitchHistory` 的 cycle detection / max switches，但不进入 vendor isolation 逻辑。
- `ProviderContinuityContext` 是 request-local 快照，由成功消费的 seed materialize 出来；共享 seed、request-local `VisibleContinuitySeedCandidate`、request-local continuity context 必须拆开，避免把跨请求共享状态和当前请求语义混在同一个对象里。

### 2. Selector 与 Eligibility
- `SelectRequest` 不再用 `FailoverContext != nil` 推断语义，改为显式传入 `SwitchMode`、`ProviderSwitchHistory` 与可选的 `ProviderContinuityContext`。
- `SelectionMetadata` 不能只保留 `strategy / sticky_continuity / active_continuity` 这种“命中路径”；还要显式携带 continuity provenance，至少包含 `ContinuitySeeded`、`ContinuityOriginProviderID`，以及可推导 `continuity_seed_age_ms` 的时间信息或等价字段。
- eligibility 拆成两层：
  - `replacement`：health、auth、routing、excludeIDs、cycle、max switches
  - `failover`：在 `replacement` 基础上，再叠加 outbound / inbound vendor isolation
- `accept_failover` / `failover_scope` 只能在 `Mode=failover` 下生效，不能影响 `replacement`。`accept_failover=any` 的现有语义保持不变，不能因为这次重构被收紧。
- 请求入口不能直接带 `ProviderContinuityContext`；入口最多只带 `VisibleContinuitySeedCandidate`。跨请求 heuristic 命中后，首个 selection 仍然只是粘回 origin provider 的 continuity re-entry；只有成功 re-entry 并 materialize request-local `ProviderContinuityContext` 后，后续离开该 origin provider 的跨 Provider 切换，才进入 `Mode=failover`。
- 如果 selection 语义依赖 hidden model，则 `VisibleContinuitySeedCandidate` 的 lookup 必须晚于 hidden model probe / resolution；禁止先用退化 `ContinuityKey` 命中 seed，再事后补 model。
- Handler / WebSocket orchestrator 不能再把 selection provenance 压成单个 `isSticky` 布尔值；观测、时间线和后续状态机要消费完整的 `SelectionMetadata`，而不是消费一个被压扁后的衍生标志。

### 3. HTTP 主链路
- `retryState.failoverContext` 改为 `switchContext`。
- 首个 Provider 失败且 `client_visible == false` 时，下一次选择标记为 `replacement`。
- `post-visible` 后禁止跨 Provider 静默切换；`pre-visible replacement` 仍按现有 selector 策略继续选下一个 Provider。
- `ActiveRequestRegistry` 继续只负责 live request / active continuity，不承载已结束请求的 continuity seed；seed 由独立的 `VisibleContinuitySeedStore` 在请求最终态里写入、查找、消费和过期。
- 只有请求满足“`client_visible == true` 且以异常连续性中断结束”时，才写入 continuity seed；正常完成、客户端主动结束、pre-visible failure 都不写 seed。
- 新的 inbound request 在首个 selection 前尝试解析 `visible continuity seed`，但 lookup 时机必须晚于构造完整 `ContinuityKey` 所需的前置步骤：若 selection 语义依赖 hidden model，则先完成 probe / resolution，再用与 selector 一致的完整 `ContinuityKey` 查 seed；禁止先用退化 key 命中 seed，再事后补 model。当前阶段使用 `best-effort heuristic`：
  - 上一轮请求必须是 `post-visible` 后异常结束
  - 窗口限制为 5 秒内
  - `ContinuityKey` 必须相同
  - 新请求首个 selection 必须先通过 sticky / active continuity 粘回 `VisibleOriginProviderID`
- 命中 key + TTL 只把该请求标记为 `continuity-candidate request`，并携带不可变候选 seed 快照；这一步还不是 continuity-attached request。
- 只有首个 selection 实际 re-entry 到 `VisibleOriginProviderID` 时，才对候选 `SeedID` 执行原子 compare-and-consume；compare-and-consume 成功后再 materialize `ProviderContinuityContext`，并将请求升级为 continuity-attached request。
- 如果首个 selection 未能 re-entry 到 `VisibleOriginProviderID`，或 compare-and-consume 失败，则放弃该候选 seed；当前请求回到普通 `initial` / `replacement`，不能预支 failover 语义。
- continuity-attached request 的首个 selection 仍是 `initial` / continuity re-entry；只有后续离开 `VisibleOriginProviderID` 的跨 Provider 切换，才以 `Mode=failover` 进入 eligibility。
- 同 provider 重试与跨 provider 切换分离建模：
  - `provider_attempt`：同一 Provider 内的重试序号，持久化到 `RequestAttempt`
  - `provider_switch_count`：跨 Provider 切换次数，仅统计 replacement / failover，不统计同 provider 重试
- `provider_switch_count` 的预算与 `attempt` 总预算分离：当前阶段不新增 runtime config，但内部语义固定为 `MaxProviderSwitches = max(0, globalMaxAttempts-1)`；`globalMaxAttempts` 继续限制总循环次数，`MaxProviderSwitches` 只限制跨 Provider 切换。

### 4. WebSocket 主链路
- `handshake rejected`、`pre-visible upstream transport error`、`pre-visible semantic suppression` 都归类为 `replacement`。
- 现有 `pre-accept failover` 命名统一改为 `pre-visible replacement` 或等价表述。
- 同一 WebSocket 会话内的 post-visible 跨 Provider 延续仍属于 `failover`。
- 跨会话 continuity heuristic 与 HTTP 共用同一个 `VisibleContinuitySeedStore` 和消费语义：只有成功 re-entry 到 origin provider 后，才 materialize request-local continuity context。
- 若 WebSocket selection 依赖 hidden model，则先完成 probe / resolution，再按完整 `ContinuityKey` 查 continuity seed；禁止拿仅含 `api_type` 的退化 key 预附着 continuity candidate。
- 跨请求 / 跨会话但命中 continuity heuristic 的续接请求，在先 re-entry 到原 Provider 后再次离开时，也属于 `failover`；Vendor Failover Isolation 需要覆盖这类场景，不能把它降级成普通 `replacement`。

### 5. 观测与 UI
- RequestAttempt / timeline 增加 `switch_mode` 或等价字段，明确区分 `replacement` 与 `failover`；不要复用现有 `switch_reason` 表达这层语义。
- `switch_reason` 继续回答“为什么从当前 attempt 切走”，`switch_mode` 回答“下一次 selection 属于 replacement 还是 failover”。
- `provider_attempt` 持久化，避免日志和 UI 依赖相邻相同 `ProviderID` 反推同 provider 重试。
- `RequestAttempt.attempt` 保持“请求内总序号”语义，继续作为存储查询和 UI timeline 的主排序键；`provider_attempt` / `provider_switch_count` 只作为补充字段，不能替代 `attempt` 的时间线职责。
- 后端 schema / API / UI 合同要一起改：`internal/model.RequestAttempt`、migration / store query、`web/src/api/types.ts`、`RequestAttemptTimeline` 同步引入 `switch_mode`、`provider_attempt`、`provider_switch_count`；排序仍保持 `attempt ASC, id ASC`。
- 当请求命中 continuity heuristic 时，日志 / timeline 需要显式记录：
  - `continuity_seeded=true`
  - `continuity_origin_provider_id`
  - `continuity_seed_age_ms`
- 当 failover 是由 continuity-attached request 触发时，日志 / timeline 需要能看出它不是“当前请求里先 visible 再切”，而是“先 re-entry 原 Provider，再离开 continuity origin 进入 failover”。
- 上述 continuity 字段必须来源于显式 `SelectionMetadata` / request-local continuity context，不能靠 `isSticky`、相邻 attempt、或最终 `provider_id` 事后反推。
- Provider 配置页文案改成：`Accept Failover` 不影响无首包失败时的 provider replacement。
- 项目文档和测试命名同步去掉“首次失败 failover”表述。

## 测试改造
- 保留“首次失败会切换 Provider”的集成测试，但断言其 mode 是 `replacement`，且不受 `accept_failover` 限制。
- 新增测试：
  - candidate `accept_failover = none/vendor` 时，首个 Provider 无首包失败后仍可被选中
  - `Mode=failover` 时，`accept_failover = any/vendor/none` 继续保持现有语义，不因本次重构变化
  - 只有 `post-visible` 后异常连续性中断才会写入 continuity seed；正常完成、客户端主动结束、pre-visible failure 不写 seed
  - key 命中 continuity seed 但尚未成功 re-entry origin provider 时，不消费 seed，也不注入 request-local continuity context
  - 新请求命中 5 秒 continuity heuristic 并先 re-entry 到 vendor A 的 origin provider 时，应被标记为 continuity-attached request
  - seed 只有在首个 selection 成功 re-entry 到 origin provider 后才会被消费；消费后当前请求仍保留 request-local continuity context
  - 同一 `ContinuityKey` 的并发候选请求里，只有首个成功 re-entry 且 compare-and-consume 成功的请求能 attach continuity；其余请求降级为普通 `initial/replacement`
  - 若 origin provider 因 health/auth/routing/concurrency 无法 re-entry，或 compare-and-consume 失败，则该请求降级为普通 `initial/replacement`，且不触发 failover isolation
  - continuity-attached request 若继续停留在 origin provider，不触发 failover isolation
  - continuity-attached request 一旦离开 origin provider，candidate `accept_failover=none` 必须被拦；`accept_failover=vendor` 仅允许匹配 vendor；`accept_failover=any` 允许
  - `replacement` 仍受 cycle detection 和 max switches 约束
  - 同 provider 重试会递增 `provider_attempt`，但不会递增 `provider_switch_count`
  - `MaxProviderSwitches` 只统计跨 Provider 切换，且当前阶段语义为 `max(0, globalMaxAttempts-1)`，不能继续直接等同于总 attempt 上限
  - `RequestAttempt.attempt` 继续作为请求内总序号和 timeline 排序键；新增字段不会改变 `attempt ASC, id ASC` 的查询 / UI 顺序
  - WebSocket handshake rejected / pre-visible transport error 属于 `replacement`
  - 当 selection 依赖 hidden model 时，continuity seed lookup 必须晚于 hidden model resolve，并使用完整 `ContinuityKey`
- failover 隔离测试保留在领域层，同时补足“跨请求 continuity seed + re-entry origin provider + subsequent leave-origin switch”的用例，避免将 Vendor Failover Isolation 收窄成仅限单次请求链。

## 验收标准
- “无首包失败”不会被 `accept_failover` 拦住。
- `initial`、`replacement`、`failover` 三个概念在类型和命名上可区分，不能继续依赖注释补救。
- 当前大多数自动跨 Provider 切换会被重新定义为 `replacement`；这是语义纠偏，不是兼容性例外。
- Vendor Failover Isolation 仍然覆盖“新请求命中 continuity heuristic、先 re-entry 原 Provider、随后切到其他 vendor”的场景，不能因为本次重构失效。
- 日志、时间线、测试断言都能看出一次切换到底是 replacement 还是 failover。
- `VisibleContinuitySeedStore` 独立于 `ActiveRequestRegistry`，并且 seed 只在成功 re-entry origin provider 后消费。
- 请求入口只能持有 `VisibleContinuitySeedCandidate`，不能直接持有 `ProviderContinuityContext`。
- 同一 seed 只能被一个请求 attach；同一 `ContinuityKey` 只保留最新未消费 seed。
- `RequestAttempt.attempt` 继续是请求内总序号；`provider_attempt` / `provider_switch_count` / `switch_mode` 只是补充维度，不改变时间线主顺序。
- `MaxProviderSwitches` 与总 attempt 预算语义分离，当前阶段固定为 `max(0, globalMaxAttempts-1)`。
- 当 selection 依赖 hidden model 时，必须先 resolve hidden model，再 lookup continuity seed。
- continuity provenance 在 selector、handler / orchestrator、日志、时间线之间保持显式传递，不能再退化成 `isSticky` 一类的压缩标志。
