# Codex 协议兼容收尾计划

## 相关信息

docs\CODEX_PROTOCOL_HEADER_HYGIENE_EXECUTION_PLAN.md
docs\CODEX_PROVIDER_COOKIE_JAR_EXECUTION_PLAN.md
docs\CODEX_WEBSOCKET_COMPATIBILITY_EXECUTION_PLAN.md

CLIProxyAPI: E:\Doraemon\IT\company\e-idea\sub2api\CLIProxyAPI

## 为什么要收尾

我本来只是想提升用户体验：让该透传的 Header 正常透传，让 HTTP/WebSocket 行为一致，并避免 Cookie 和状态跨 Provider、跨账号泄漏。

结果实现膨胀成了一套需要用户手工准备 keyring、理解四个内部开关，还会拒绝正常 `response.append` / `response.inject` 的复杂系统。它甚至把测试 fixture 写进生产路径，并顺带改变了无关请求的重定向行为。甚至搞了客户端硬编码表。你要让用户codex一升级就用不了吗？

**这完全偏离了最初目标，我对此很生气。** 收尾工作的目的不是继续给复杂设计打补丁，也不是撤销已经实现的那部分合理的代码，而是把产品拉回“默认可用、透明兼容、只在真实跨边界时拦截”的正轨。

## 最终原则

- 不使用统一状态边界，按载体处理：

| 载体 | 边界 |
| --- | --- |
| 已识别身份、Turn State、Turn Metadata、`previous_response_id` | `ClientScope + ProtocolScope`；RouteTarget 仅作路由偏好，不绑定 connection generation |
| `response.append`、`response.inject` | 当前上游 WebSocket 连接；禁止跨连接重放和 replacement |
| Provider Cookie | `JarID + CookieAuthority` |
| `X-Oai-Attestation` | 当前 operation 的 Authority |
| 未识别 metadata、事件、非 JSON 文本帧和二进制帧 | opaque 原样转发，不创建 owner、不改变路由 |

- 只有已识别控制事件中已有协议证据确认的关键字段非法时才拒绝；未知内容不得因解析器不认识而失败。
- 认证 Header、hop-by-hop Header、Cookie 和握手协商字段仍由执行器重建或接管。
- 不再让用户理解内部模块、依赖顺序和发布开关。
- 不为本次收尾引入完整 transcript 引擎、更多协议版本目录或新的灰度体系。
- 保留原计划的核心体验，去除过度设计过度安全的东西。


## README 承诺的是

- 尽可能透明转发，避免脆弱的协议转换：[README-ZH.md](E:/Doraemon/IT/Repository/switch-a/README-ZH.md:13)
- 没有魔法字符串，上游升级通常不需要更新 switch-a：[README-ZH.md](E:/Doraemon/IT/Repository/switch-a/README-ZH.md:14)
- “全网最佳”的 GPT WebSocket 适配、故障切换和会话连续性：[README-ZH.md](E:/Doraemon/IT/Repository/switch-a/README-ZH.md:15)
- 大部分配置通过管理界面完成：[README-ZH.md](E:/Doraemon/IT/Repository/switch-a/README-ZH.md:25)

## P0：恢复兼容性

1. 先增加 `.gitattributes`，将 `internal/codex/headers/testdata/**` 固定为 LF，并统一现有 fixture 字节。
2. 删除生产路径中的 `FixtureCodexDesktop0150Alpha8`；运行时只识别稳定字段，未知事件、非 JSON 文本帧和二进制帧原样转发。
3. `previous_response_id` 只按 `ClientScope + ProtocolScope` 校验；不因连接或 RouteTarget 变化主动拒绝，相同 ProtocolScope 内可在客户端可见前重新选路，但不承诺上游一定接受跨连接续接。
4. 支持 `response.append`：要求当前上游连接存在，不要求 active response；原帧转发，禁止跨连接重放和 replacement。
5. 支持 `response.inject`：不猜测 `response_id` 路径，只按 connection-bound 控制帧原样转发，由上游校验目标；禁止跨连接重放和 replacement。
6. 客户端帧的协议决策必须在写入 replay buffer 前完成，并返回明确的重放/replacement 策略。
7. 普通 WebSocket 默认走非 Probe 路径：向上游提供完整客户端 subprotocol offer，采用上游真实选择，再把该选择和可传递的 101 Header 返回客户端。
8. 普通 101 只提交握手协商，不固定 Authority 或 RouteTarget；只有 `X-Codex-Turn-State` 成功投射，或首个上游应用帧成功写入客户端后才固定。
9. Probe 仅在必须读取客户端数据才能选路时使用；以 3 秒、128 帧和 4 MiB 为总上限，缓冲到第一条 `response.create` 并按原顺序转发。Probe 按客户端顺序固定首个 subprotocol，目标上游必须精确匹配；无匹配目标时明确失败，不得静默退回普通选择。
10. HTTP 和 WebSocket 共用确定性的 owner 偏好归并：一旦出现不同 RouteTargetHint，当前请求永久清空偏好，不能被后续遍历恢复。
11. response 生命周期至少处理现有证据确认的 `response.completed`、`response.incomplete` 和 `response.failed`；不得从外部文档追加未证实事件。
12. 普通 HTTP 请求恢复自动跟随重定向；只有服务端 Cookie Jar 接管的 Codex 请求返回原始 3xx。该选择属于请求执行策略，不属于 Header policy。
13. 恢复 subprotocol、Content-Type、Content-Encoding 和状态决策等非敏感诊断值。
14. 固定状态冲突、需要重连、需要新 Thread、存储故障对应的 HTTP status/error code、WS close code 和恢复动作。
15. 明确 maintenance Stop 的 deadline 语义：调用时 Context 已过期则触发停止并稳定返回 `ctx.Err()`。

## P1：恢复零配置体验

1. 四个运行时开关全部删除：同时移除持久 key/default、动态 FeatureSource、依赖校验、管理 API、导入导出和前端复选框，不保留“忽略旧值”的恢复路径。
2. Header Hygiene、WebSocket subprotocol、Continuity 和 Provider Cookie Jar 由运行时能力无条件组成。
3. 固定启动顺序：解析 keyring 路径；无 signer 打开并迁移数据库；检查所有 CredentialSubject、Continuity HMAC、Cookie HMAC、Cookie AEAD 历史 key version，以及所有 pending 静态凭据 subject。
4. 已有 keyring 必须成功解析并覆盖全部历史版本；只有文件不存在且历史引用为零时才原子生成。损坏或不完整文件不得自动覆盖。
5. 注入 keyring，并事务性完成所有 pending 静态 API Key subject；ChatGPT reauth pending subject 保持原状态。
6. 最后构造 runtime、启动后台任务和 listener。
7. Provider Cookie 继续按 JarID + CookieAuthority 隔离，遵守 Domain、Path、Secure 和过期规则；原始 Provider `Set-Cookie` 不直接暴露给客户端。

## 必须保留的实现

- 每次物理尝试重新构造认证 Header，只注入当前 Provider 身份。
- CredentialSession、AppliedIdentity、ClientScope、Authority 和 ProtocolScope 边界。
- owner 的 pending → committed 可见性提交。
- `X-Codex-Turn-State` 成功投射或首个上游应用帧成功写入后固定 RouteTarget；普通 101 不固定。
- Cookie Jar 的逐 Cookie 合并、持久化和跨 Authority 隔离。
- 非 Probe 路径的真实上游 subprotocol 与 `X-Codex-Turn-State` 投影。

## 验收标准

- 只使用仓库现有 `.capture`/fixture 和 CLIProxyAPI 行为作为协议依据，不要求新增抓包或真实客户端测试。
- HTTP、SSE、WebSocket 的 create、append、inject、previous response、compaction 和断线重连正常。
- `Cookie`、`X-Codex-Turn-State`、`X-Codex-Turn-Metadata`、`X-Oai-Attestation` 在 HTTP/WS 中按相同边界处理。
- 同一安全范围内不因未知字段或未识别事件失败。
- 普通 101 后、首个可见状态或数据前的故障仍可重选；提交 Turn State 或首帧后不得切换 RouteTarget。
- Probe 能处理 `response.create` 前的其他帧；owner 偏好在不同遍历顺序下结果一致。
- 两个客户端凭据、两个 Provider、两个账号之间的 Cookie、Response ID 和连续性状态不能串用。
- 普通用户替换二进制后无需手写 keyring，也无需研究四个开关。
- `make ci` 和 maintenance Stop 重复测试通过。

## 完成定义

只有当正常客户端默认可用、该透传的数据确实透传、跨账号状态确实隔离，并且用户不再承担内部架构复杂度时，这次工作才算完成。
