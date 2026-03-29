## Open Questions

1. “Routing Policies 需要能够指定每某一个具体 provider 的能力” 这里的“能力”具体指什么：是要在规则里直接选择允许的 `provider`，还是要先给 `provider` 定义独立于 `api_types` 的能力标签/特性，再让规则按这些能力筛选？
Answer: 直接选择具体 provider，不引入额外能力标签体系。

2. 如果路由规则后续同时支持 `group`、`vendor`，以及新的 provider/能力约束，这些条件应继续取交集吗？当交集为空时，是否仍应直接失败而不是回退到默认选路？
Answer: 选择具体 provider 时，不允许同时再选择 group 或 vendor。provider 是最小原子选项，也是终态约束，因此这类交集语义不成立。

3. `Allowed Vendors` 的可选项应该来自哪里：仅使用当前已配置 provider 的非空 `vendor` 值，还是需要维护一份独立的 vendor 目录？如果当前没有目标 vendor，是否允许在该页面直接新增选项？
Answer: 仅从当前已有 provider 的非空 vendor 值中选择，不额外维护独立 vendor 目录。

4. 规则“停用/启用”的语义是否为：停用后配置仍保留且可编辑/恢复，但运行时完全忽略？另外，停用规则后，是否允许再创建一条相同 `api_type + model` 键的新启用规则，还是停用规则仍然占用唯一键？
Answer: 停用后规则仍保留、可编辑、可恢复；恢复后重新生效。停用后对后续新连接应立即失效。停用规则后依然不允许创建冲突规则，唯一键仍被占用。

5. 如果某个 `api_type` 当前没有 active routing rules（包括规则全部被禁用），运行时应该回退普通选路，还是继续按“受 routing policy 治理”处理并 fail-closed？
Answer: 没有 active rules 时回退普通选路。只有存在 active rules 且当前请求没有匹配到任何 active rule 时，才 fail-closed。

6. WebSocket probe 被禁用时，或者请求当前没有可用 `model` 时，是否还要为了 model-specific routing rule 继续探测 hidden model？
Answer: 不需要。probe 配置被禁用时必须优先遵循禁用配置，不做补偿性探测。请求没有可用 `model` 时，`exact` / `prefix` 这类 model-specific rule 视为未匹配；若因此没有任何 active rule 匹配，则回退普通选路。

## Design Decisions

- 具体 provider 选择与 group/vendor 选择互斥。UI、API、持久化约束和运行时语义都应体现这一点，以避免出现多重范围约束的歧义。
- 如果某个 group 被 routing policy 引用，删除该 group 也必须返回 `409 Conflict`，直到相关规则被修改或删除。
- 如果某个 provider 被 exact-provider 规则引用，删除该 provider 必须返回 `409 Conflict`，直到相关规则被修改或删除。
- Routing Policies 本次必须纳入 config export/import。
- Config import 必须全有或全无：先基于 staged catalog 完成校验，再通过单个 store-level transaction / unit-of-work 一次性应用，禁止部分成功。
