# Codex Provider Cookie Jar 执行计划

> 复用 `internal/codex/identity` 的 ClientScope、UpstreamAuthority 和 AppliedIdentity 校验；Cookie Jar 使用独立发布开关，不依赖状态连续性开关是否启用。

## 目标

让 Codex HTTP 与 WebSocket 安全复用上游 Cookie，同时保证 Cookie 永不跨客户端、上游 Authority 或 ChatGPT 账号。

不直接转发客户端原始 `Cookie` 或上游原始 `Set-Cookie`；客户端只持有 switch-a 的匿名 Jar Handle，真实 Cookie 由服务端按上游语义管理。

## 所有权模型

- `ClientScope`：由 `internal/codex/identity` 计算客户端 HMAC，用于校验 Handle 归属。
- `JarID`：服务端随机生成的持久 Jar 命名空间；客户端 Handle 只用于查找绑定当前 ClientScope 的 JarID，不能由客户端输入推导。
- `CookieScope`：`JarID + UpstreamAuthority`，其中 Authority 为 `Vendor + normalized UpstreamOrigin + CredentialSubject`，不含 APIType 或 ProviderID。Origin 复用统一规范化规则，使 `wss/https` 和 `ws/http` 分别落入同一 Authority。
- `CookieKey`：`Name + Domain + Path`，允许同名 Cookie 在不同 Domain/Path 下并存。

Handle 缺失、未知、过期或 ClientScope 不匹配时签发新 Handle 和空 JarID，绝不关联或复制旧 Jar。客户端 API Key 轮换形成新的 ClientScope，同样签发空 JarID。

客户端 Handle Cookie 删除 `Domain`，使用覆盖 Codex HTTP/WS 路由的最小公共 `Path`，设置 `HttpOnly` 和 `SameSite=Lax`，可信外部连接为 HTTPS 时设置 `Secure`。`Path` 只缩小客户端发送范围，不承担 Authority 隔离。

## 实施步骤

### 1. 建立 Cookie 深模块

新增 `internal/codex/cookie`，封装 RFC Cookie 的 Domain、Path、Secure、Expires、Max-Age 和删除语义。存储接口按使用方定义，SQLite 使用持久、版本化的 AEAD 密钥加密 Cookie 值，关联数据为 `JarID + Authority + CookieKey`，并提供过期、孤立 Authority 清理和容量上限。任何需要读写 Jar 的操作遇到密钥不可用或解密失败时返回显式错误，禁止读取、注入、覆盖或静默降级为空 Jar。

所有限制使用命名常量。

### 2. 注入请求 Cookie

HTTP 与 WebSocket 都在 Provider 选择和凭据注入完成后通过有效 Handle 取得 JarID，并通过 `internal/codex/identity` 取得和校验 `AppliedIdentity`、解析 CookieScope，从持久 Jar 与当前逻辑请求的同 Scope overlay 中按实际上游 URL 选取 Cookie，再生成上游 `Cookie` Header。

删除客户端原始 `Cookie` 后再注入 Jar 结果。无效 Handle 使用本次新建的空 JarID；CookieScope 不匹配时加载当前 Scope 的 Jar，不复制其他 JarID 或 Authority 的 Cookie。

Cookie Jar 开启时，Codex HTTP 上游请求禁止自动重定向，3xx 原样返回协调层并按普通最终响应或重试边界处理；本计划不实现逐跳 redirect coordinator。

### 3. 接收响应 Cookie

每个逻辑请求维护独立 overlay。上游响应的 `Set-Cookie` 先写入当前 CookieScope 的 overlay；只有同 CookieScope 的后续尝试可以读取，切换 Scope 不复制 Cookie。

尝试成为最终响应或客户端可见边界时，无论状态码，按 CookieKey 将 overlay 逐项事务合并到持久 Jar，禁止整 Jar 覆盖。被替换、切换 Scope，或仅发生本地失败且没有形成最终上游边界时丢弃对应 overlay。

上游 `Set-Cookie` 不直接返回客户端。客户端没有有效 Jar Handle 时，只返回 switch-a 自己绑定新 JarID 的 Handle Cookie；原始 Domain/Path 保存在 Jar 中，用于以后匹配实际上游 URL。

### 4. 兼容 probe 与 failover

probe 在下游升级前创建并返回 Jar Handle，随后取得的上游 `Set-Cookie` 仍可写入服务端 Jar，因此不依赖向已完成的 `101` 补 Header。

同 Authority 重试或切换 RouteTarget 时复用 Jar；Vendor、CredentialSubject 或 Origin 变化时加载另一 Jar，绝不复制旧 Cookie。Provider 删除只清理已无 RouteTarget 可达的 Jar。

## 验证

- 纯函数测试覆盖 Domain/Path/Secure/过期/删除、同名多路径和 Header 大小限制。
- 所有权测试覆盖相同 API Key 下不同 JarID 的隔离、Handle 归属校验、同 Authority 的 RouteTarget 切换，以及 Vendor/CredentialSubject/Origin 变化。
- HTTP 测试覆盖同 Scope 重试读取 overlay、最终响应逐项合并、重试丢弃、Handle 下发和下次请求回送；缺失、未知、过期、ClientScope 不匹配及 API Key 轮换都覆盖签发新 Handle 和空 JarID。
- 重定向测试确认启用 Cookie Jar 的 Codex HTTP 不自动跟随 3xx，中间响应按协调层边界处理。
- WebSocket 测试覆盖普通握手、probe、pre-visible replacement、同 Scope overlay 和已提交边界。
- 并发测试确认不同请求按 CookieKey 合并，不以旧快照覆盖整个 Jar。
- 安全测试确认未知 Cookie 不上游透传，数据库中的 Cookie 值按设计加密读写。
- 最终运行 `make ci`，保持 Go 覆盖率不低于 90%。

## 完成标准

持有有效 Handle 的客户端可跨 HTTP/WS 和同 Authority 的 RouteTarget 切换延续 Cookie；probe 不影响 Cookie 保存；新 Handle 永远得到空 JarID，任何 JarID 或 Authority 变化都无法读取或发送其他 CookieScope 的 Cookie。
