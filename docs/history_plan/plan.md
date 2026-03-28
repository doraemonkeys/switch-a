# Provider 认证状态与硬路由重构计划

## Status: Completed

## 目标

- 拆开 `provider.enabled`、认证生命周期、运行时健康、路由约束四类语义，消除当前状态混叠。
- 解决 ChatGPT/Codex 凭证失效误判、`credential_data` 并发覆盖、`auth_profile.ready` 误导性语义。
- 让 `GET /admin/api/providers` 与其他管理读接口保持纯读、低延迟，不在读路径里触发外部网络请求或状态写回。
- 支持按 `api_type` 与可选 `model` 匹配的硬约束路由。
- failover 必须留在约束闭包内，不允许自动逃逸到外部 group 或 vendor。

## 已确认事实

- `provider.enabled` 表示管理员意图，不应被运行时错误自动改写。
- `health` 表示临时不可用、冷却和自动恢复，不适合表达“必须重新登录”。
- `auth_profile.ready` 只表示凭证字段完整，不表示凭证当前有效。
- 当前 `credential_data` 会被 token refresh 与 usage snapshot 两条路径整块回写，存在并发覆盖风险。
- `api_type` 已能从 HTTP 路径稳定解析，provider 已通过 `api_types` 声明支持的 upstream contract。
- WebSocket 目前只支持 `codex`。握手阶段模型一律视为未知；后续可能在 `session.created` / `response.created` 语义消息里解析出模型，但这不影响握手选路。
- 当前 `GET /admin/api/providers` 会串行调用 `PopulateProviderAuthProfile`，而该路径会执行 `ensureFreshChatGPTCredential` 与 `fetchChatGPTUsageSnapshot`，因此列表接口会被外部 OAuth/usage 请求拖慢。

## 设计决策

- 采用正交拆层，而不是继续在 `providers` 与 `health` 上追加语义。
- 持久化认证状态采用三态：`not_connected`、`active`、`reauth_required`。
- 不持久化 `transient_error`。临时 refresh 失败、网络抖动、上游 5xx/超时继续走 `health` 与诊断字段。
- `reauth_required` 只能由成功重新登录解除，不能自动恢复。
- ChatGPT provider 已创建但尚未完成登录时，状态为 `not_connected`，不与 `reauth_required` 混用。
- 系统不得因为认证失效自动把 `provider.enabled` 改成 `false`。
- `auth_profile.ready` 不再承担“当前可用”语义；管理 API 改为显式返回认证状态。
- 管理读接口必须是纯读语义：禁止在 `GET` 请求里做 token refresh、usage refresh、credential 持久化或其他外部副作用。
- 路由策略按 `api_type` 精确匹配，并可附加 `model` 匹配条件，使用硬约束 `allowed_group_ids` 和 `allowed_vendors`。
- 同时存在 group 与 vendor 约束时取交集；约束后无候选则直接失败。
- 某个 `api_type` 未配置任何 `RoutingPolicy` 时，保持现有默认选路行为；只有该 `api_type` 存在规则时才进入硬约束模式。
- failover 只能发生在约束后的候选集合内，不自动逃逸到外部。
- 模型级路由仅在选路前模型已知时生效；若模型未知，则只匹配不依赖模型的规则。
- `RoutingPolicy` 是对现有 `FailoverScope` / `AcceptFailover` 的叠加约束，不是替换。先用 `RoutingPolicy` 收缩候选集，再在收缩后的集合内执行现有 failover 规则。
- 所有选路入口必须复用同一个 eligibility 判定：主 selector、sticky cache、active request fallback、以及 `selector == nil` 时的 fallback 路径都不能绕过 `RoutingPolicy` 与 `auth_state` 校验。
- 管理 API 移除旧 `auth_profile.ready` 契约，改为显式 `auth` 响应对象表达认证类型、状态、原因和摘要。
- `providers.credential_data` 只作为一次性迁移源；迁移完成后所有新写入只落 `provider_credentials`，最终删除旧列。
- continuity key 必须按“当前请求在选路前已知的维度”生成，而不是依赖单个全局 `perModel` 开关。
- WebSocket 上若选路前模型已知，则 `StickyModeModel` 保持 model 维度；若模型未知，则降级为 `api_type` 级 sticky。

## 目标架构

### 1. ProviderConfig

职责：
- provider 静态配置
- `group_id` / `vendor`
- `api_types`
- 管理员启停

落点：
- `providers`
- `provider_api_types`

### 2. ProviderCredential

职责：
- 敏感 token / refresh context
- 只存真正的 secret material
- 提供原子更新能力

落点：
- 新表 `provider_credentials`

关键要求：
- `provider_id` 一对一
- 增加 `binding_account_id`（或等价绑定键）用于表达 ChatGPT 账号归属，并建立唯一约束，继续保证一个上游账号只能绑定一个 provider
- 增加 `version`，所有写入走 CAS
- usage snapshot 不再写回 secret blob

### 3. ProviderAuthState

职责：
- 认证生命周期
- 非敏感认证摘要
- refresh 诊断信息

落点：
- 新表 `provider_auth_states`

建议字段：
- `provider_id`
- `status`
- `status_reason`
- `last_error`
- `last_transition_at`
- `email`
- `account_id`
- `plan_type`
- `expires_at`
- `last_refresh_at`
- `usage_snapshot`
- `refresh_fail_count`
- `last_refresh_failure_at`

说明：
- API key provider 默认保持 `active`
- ChatGPT provider 未完成登录时为 `not_connected`
- OAuth / ChatGPT provider 通过该表表达是否需要重新登录

### 4. HealthState

职责：
- circuit breaker
- cooldown
- 自动恢复
- 暂时性不可用

落点：
- 继续使用 `health_states`

说明：
- 认证终态失效不再塞进 `health`

### 5. RoutingPolicy

职责：
- 按请求类型限制可选 provider 集合
- 独立于认证和健康

落点：
- 新表 `routing_policies`
- 辅助表 `routing_policy_groups`
- 辅助表 `routing_policy_vendors`

规则：
- `api_type` 精确匹配
- 可选 `model_match_type`
- 可选 `model_match_value`
- 产出 `allowed_group_ids`
- 产出 `allowed_vendors`
- 均为硬约束

建议支持的模型匹配：
- `exact`
- `prefix`

匹配规则：
- 选路前模型已知时，同时匹配 `api_type` 与可选 `model`
- 选路前模型未知时，只能命中不带模型条件的规则
- 某个 `api_type` 不存在任何规则时，不做 `RoutingPolicy` 过滤

冲突解析：
- 最具体优先：`exact` 优先于 `prefix`，带 `model` 条件的规则优先于仅 `api_type` 的规则
- 多条 `prefix` 同时命中时，前缀更长者优先
- 模型未知时，只 fallback 到 `api_type` 级规则
- 禁止同一 `(api_type, model_match_type, model_match_value)` 组合出现多条规则；写入时校验唯一性
- 若两条规则在同一请求上仍无法比较出唯一“更具体”者，则拒绝写入，避免运行时歧义

### 6. ProviderSelectionEligibility

职责：
- 统一定义“某个 provider 当前是否可参与此次请求选路”
- 收敛 `enabled`、`api_type`、`RoutingPolicy`、`auth_state`、`health`、failover/continuity 校验
- 消除主选路、sticky、active fallback、selectorless fallback 之间的语义漂移

落点：
- selector / proxy 共享的纯函数或小型服务

要求：
- 主 `selector` 选路必须复用
- `checkStickyCache` 校验 cached provider 时必须复用
- `tryActiveProviderFallback` / `getProviderIfValid` 必须复用
- `selector == nil` 时的 `selectProviderFallback` 也必须复用

### 7. ProviderAuthView

职责：
- 管理 API 返回给前端的显式认证视图
- 独立于 secret 存储与 refresh 逻辑
- 消除 `auth_profile.ready` 的旧兼容语义

落点：
- provider 响应上的非持久化 `auth` 字段

建议字段：
- `type`
- `status`
- `reason`
- `email`
- `account_id`
- `plan_type`
- `usage`
- `expires_at`
- `last_refresh_at`
- `last_error`

## 核心行为

### 认证状态判定

- ChatGPT provider 已创建但没有完整登录凭证时，进入 `not_connected`
- 显式登录成功后，`not_connected` / `reauth_required` → `active`
- 命中 `refresh_token_reused`、`invalid_grant`、明确要求重新登录的错误时，进入 `reauth_required`
- refresh 超时、网络错误、5xx 不进入 `reauth_required`
- refresh 成功只更新 `active` 状态 provider 的 credential，不复活已判定为 `reauth_required` 的记录
- `reauth_required` 只能由显式重新登录成功解除（与第 26 行一致）

### Admin 读路径

- `GET /admin/api/providers` 只读取本地存储中的 provider、health、auth state、usage snapshot
- 列表接口不触发 `ensureFreshChatGPTCredential`
- 列表接口不触发 `fetchChatGPTUsageSnapshot`
- 列表接口不执行任何 credential 或 auth state 写回
- 单 provider 详情接口也默认保持纯读
- 需要主动同步时，使用显式动作接口或后台任务，而不是在 `GET` 中隐式刷新（接口定义见 Phase 4）

### 并发与一致性

- 同一 provider 的 refresh 做 singleflight / 互斥
- `provider_credentials` 更新必须带 `version`
- CAS 失败时先重读再决策，不允许盲写覆盖
- usage snapshot 更新只写 `provider_auth_states`，不再回写 secret

### Selector 选路

候选条件统一为：

- 若 `req.api_type` 存在 `RoutingPolicy`，则先按 `api_type` 与可选 `model` 收缩候选集
- `api_type` 与可选 `model` 匹配 `RoutingPolicy`
- `provider.enabled = true`
- `auth_state = active`
- `health.available = true`

说明：
- HTTP 与 WebSocket 共用同一套候选过滤语义
- 主 selector、sticky 校验、active request fallback、selectorless fallback 复用同一个 eligibility 判定
- failover 只能在过滤后的集合里继续选择
- `RoutingPolicy` 先收缩候选集，再在收缩后的集合内执行现有 `FailoverScope` / `AcceptFailover` 规则
- 若 `req.api_type` 没有任何 `RoutingPolicy`，则跳过该步，保持当前默认候选语义

### Sticky / Active Request Fallback

- sticky cache 命中后，必须校验该 provider 仍满足当前请求的 `RoutingPolicy` 与 `auth_state = active`；不满足则淘汰缓存条目，回退到正常选路
- active request fallback（`tryActiveProviderFallback`）同样必须校验 `RoutingPolicy` 与 `auth_state`；不满足则跳过，不返回该 provider
- continuity key 必须按请求维度生成，而不是仅由全局 sticky mode 切换 registry key 形状
- HTTP 在 `StickyModeModel` 下，若模型已知，则 key 包含 model；若模型未知，则退化为 `api_type`
- WebSocket 的 sticky key 与 active request fallback key 也只基于握手阶段已知维度；若握手时模型已知，则保留 model；未知时退化为 `api_type`
- 后续解析出的模型只用于日志、观测和状态补全，不用于重写 WebSocket sticky key 或 active request fallback 索引

### WebSocket

- WebSocket 首次选路使用握手时已知的信息
- 当前实现里握手模型固定未知，因此 WebSocket 首次选路目前只会匹配 `api_type` 级规则
- 若未来握手阶段能可靠拿到模型，则 `RoutingPolicy` 与 `StickyModeModel` 都直接使用该模型，不额外降级
- 后续从语义消息解析出的模型只用于日志、观测和状态补全，不触发中途改路由，也不改变 sticky 维度

## 实施步骤

### Phase 1: Schema 与迁移 ✅ Done

- 新增 `provider_credentials`
- 为 `provider_credentials` 增加 `binding_account_id`（或等价绑定键）并建立唯一约束
- 新增 `provider_auth_states`
- 新增 `routing_policies` 及其关联表
- 为现有 provider 回填 credential 与 auth state
- 为现有 ChatGPT provider 回填三态 auth state：未完成登录 → `not_connected`，可用凭证 → `active`
- 将现有非敏感 usage / account 摘要迁移出 secret blob

### Phase 2: Config Import/Export 迁移 ✅ Done

- `ConfigExportVersion` 升版（`"1.0"` → `"2.0"`）
- `ExportedProvider` 移除 `credential_data` 字段，新增 `credential` 子结构（对应 `provider_credentials` 表字段）和 `auth_state` 子结构（对应 `provider_auth_states` 非敏感摘要）
- `buildExportedProvider` 从新表读取 credential 与 auth state，组装新导出格式
- `buildProviderFromExport` 解析新格式，分别写入 `provider_credentials` 与 `provider_auth_states`
- 导出默认保持全量导出，包含 secret credential material，确保配置备份可完整恢复
- 导入时若 `version` 不是 `"2.0"` 则拒绝并返回明确错误，不做老格式兼容
- 更新 `providerImportDiffers` 比较逻辑以覆盖新字段
- 更新所有 import/export 测试断言

### Phase 3: Auth 服务重构 Ô£à Done

- 读取凭证改为从 `provider_credentials` 获取 Ô£à Done
- `BuildAuthProfile` 退役，改为从 `provider_auth_states` 生成显式 `auth` 视图 Ô£à Done
- refresh 成功后分别更新 secret 与非敏感摘要 Ô£à Done
- 终态认证失败写入 `reauth_required` Ô£à Done
- 未完成登录的 ChatGPT provider 维持 `not_connected` Ô£à Done
- 拆分“纯读取 auth summary”和“主动 refresh/sync”两类服务方法，禁止列表接口复用带外部请求的 profile 填充路径 Ô£à Done
- Phase 2 完成后，停止写 `providers.credential_data`，仅保留迁移期读取能力 Ô£à Done

### Phase 4: Selector 与 Proxy 重构 ✅ Done

- ✅ Done: 引入共享 `ProviderSelectionEligibility` 判定，并让 selector / proxy 所有入口复用
- ✅ Done: selector 加入 `RoutingPolicy` 过滤
- ✅ Done: selector 加入 `auth_state` 过滤
- ✅ Done: `checkStickyCache` 增加 `RoutingPolicy` 与 `auth_state = active` 校验；不满足则淘汰缓存条目，回退正常选路
- ✅ Done: `tryActiveProviderFallback` → `getProviderIfValid` 增加 `RoutingPolicy` 与 `auth_state = active` 校验；不满足则跳过
- ✅ Done: `selectProviderFallback`（`selector == nil`）同样复用 eligibility 判定，不保留旁路语义
- ✅ Done: continuity key 改为按请求维度生成，移除对单个全局 `perModel` 开关的架构依赖
- ✅ Done: `RoutingPolicy` 先收缩候选集，再在收缩后的集合内执行现有 `IsFailoverAllowed`（`FailoverScope` / `AcceptFailover`）
- ✅ Done: HTTP / WebSocket 共用一致的候选过滤与 failover 闭包
- ✅ Done: 删除将认证问题误塞到 `health` 的路径

### Phase 5: Admin API / UI

- 管理 API 返回显式 `auth` 对象，不再返回带 `ready` 语义的旧 `auth_profile` Ô£à Done
- provider 校验不再以 `ready` 作为“当前可用”判断 Ô£à Done
- UI 直接展示 `auth.status = not_connected / active / reauth_required`
- 增加按 `api_type + model` 配置硬路由策略的管理入口
- `GET /admin/api/providers` 改为只返回本地快照 Ô£à Done
- 如需手动同步，新增两个独立的显式动作接口： Ô£à Done
  - `POST /admin/api/providers/{id}/refresh-credential`：触发 OAuth token refresh，更新 `provider_credentials` 与 `provider_auth_states` Ô£à Done
  - `POST /admin/api/providers/{id}/refresh-usage`：拉取用量快照，只更新 `provider_auth_states.usage_snapshot` Ô£à Done
- 两者频率、失败语义、可接受延迟不同，不混成一个操作，不在列表接口里隐式触发 Ô£à Done

### Phase 6: 清理旧模型 ✅ Done

- 删除 `providers.credential_data` 列，彻底移除旧 secret 主源
- 删除旧 `auth_profile` 响应模型与 `ready` 语义
- 清理只为旧模型存在的兼容代码

## 测试要求

- 迁移回填测试
- `not_connected` / `active` / `reauth_required` 三态迁移与状态流转测试
- refresh 与 usage snapshot 并发写测试
- `refresh_token_reused` 竞争与终态判定测试
- `reauth_required` 只能由重新登录解除的测试
- ChatGPT provider 未完成登录时保持 `not_connected` 的测试
- selector 对 `enabled` / `auth_state` / `health` / `routing_policy` 的组合过滤测试
- `api_type` 未配置任何 `RoutingPolicy` 时保持当前默认选路行为的测试
- 固定 group / 固定 vendor 下 failover 不逃逸测试
- HTTP 请求按 `api_type + model` 命中硬路由的测试
- `RoutingPolicy` 优先级：`exact > prefix > api_type-only` 的测试
- 多条 `prefix` 同时命中时更长前缀优先的测试
- 规则重叠但无法比较具体性的写入被拒绝测试
- WebSocket 握手模型固定未知时按 `api_type` 约束正确选路的测试
- `GET /admin/api/providers` 在多 provider 场景下不发生外部 HTTP 调用的测试
- sticky cache 命中但 `RoutingPolicy` 不满足时淘汰缓存并回退正常选路的测试
- active request fallback 命中但 `auth_state = reauth_required` 时跳过的测试
- `selector == nil` 时 fallback 路径仍服从 eligibility / `RoutingPolicy` / `auth_state` 的测试
- continuity key 在 HTTP `StickyModeModel` 下模型已知时按 model、生效前未知时退化为 `api_type` 的测试
- WebSocket 在 `StickyModeModel` 下：握手模型未知时退化为 `api_type`，握手模型未来若已知则保持 model 的测试
- 同一 `(api_type, model_match_type, model_match_value)` 重复规则写入被拒绝的测试
- `RoutingPolicy` 收缩候选集后 failover 仍服从 `FailoverScope` / `AcceptFailover` 的测试
- ChatGPT 账号唯一绑定约束在 create / update / refresh 路径都成立的测试
- config export 输出 `version: "2.0"` 且包含新 credential/auth_state 子结构的测试
- config export 默认全量导出并包含 credential secret 的测试
- config import 拒绝 `version: "1.0"` 并返回明确错误的测试
- config import/export round-trip 保真测试（覆盖 ChatGPT credential 场景）
- credential refresh 与 usage refresh 两个显式接口独立调用的测试
- refresh 成功不复活 `reauth_required` provider 的测试

## 明确不做

- 不在 WebSocket 会话建立后依据新解析出的模型重新选 provider
- 不保留 `ready` 的旧兼容语义
- 不兼容 `version: "1.0"` 的导入格式
