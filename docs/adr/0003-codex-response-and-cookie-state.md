# ADR-0003：Codex 响应可见性与 Provider Cookie 状态

- 状态：接受
- 日期：2026-08-26
- 影响任务：K0、CC2、PC2、W3-HTTP、W3-WS、DB2

## 背景

现有 HTTP `onCommit` 在底层 `Write`/`WriteHeader` 之后才运行，`Flush` 还能绕过它；这无法保证 continuity pending 和 Cookie merge 先于客户端可见。Cookie 还需要明确外部 HTTPS、并发覆盖顺序、TTL 与容量，否则不同 adapter 会形成不同安全语义。

## 决策

### 1. 外部 HTTPS 采用可证明的三态判定

外部 scheme resolver 返回 `https`、`http` 或 `unknown`：

1. `request.TLS != nil` 时为 `https`，不再让转发 Header 覆盖直接 TLS 事实。
2. 非 TLS 且 immediate peer IP 不在 startup 配置 `trusted_proxy_cidrs` 时，忽略所有 `Forwarded`/`X-Forwarded-Proto`，按直接 `http` 处理。空 CIDR 列表即完全不信任代理 Header。
3. 非 TLS 且 peer 受信时，可信代理必须覆盖而不是追加外部 scheme：只接受单个 `Forwarded` element 中唯一的 `proto=http|https`，或单个、不含逗号的 `X-Forwarded-Proto: http|https`。两者同时存在必须一致；缺失、重复、冲突或非法值为 `unknown`。

Cookie Jar 请求遇到 `unknown` 时在发放/接受 handle 前失败。gateway handle 仅在结果为 `https` 时设置 `Secure`；直接 HTTP 可发非 Secure handle。上游 Cookie 的 Secure 匹配只看物理 upstream URL 的 `https/wss`，不看外部 scheme，也不从已折叠的 Authority 反推。

gateway Cookie 契约固定为：名称 `switch_a_codex_jar`，32 bytes CSPRNG 后用无 padding base64url 编码；JarID 使用另一份独立 32 bytes 随机值。Cookie 无 Domain、`Path=/`、`HttpOnly`、`SameSite=Lax`，Secure 按上述规则。缺失、malformed、多值、未知、过期或 ClientScope mismatch 均原子创建新 handle + 新空 Jar，外部不区分原因。

### 2. HTTP 只有一个 pre-commit gate

final response owner 在 attempt coordinator，不在 body analyzer 或通用 Store。所有可能产生下游 bytes 的接口都必须经过同一个 once gate，包括显式/隐式 `WriteHeader`、`Write`、`Flush`/`FlushError`、`ReadFrom`；feature 路径不暴露可绕过 gate 的 Hijack。gate 顺序为：

1. 冻结 final attempt、status、sanitized upstream headers 与 gateway-owned headers；discarded/local-only/scope-switch attempt 不进入 gate。
2. 完成所有纯校验与 response state/ref 提取。
3. 为可见的 continuity 值创建 durable `pending`。任一失败时尚未调用底层 writer。
4. 在逐 CookieKey 事务中 merge final Cookie overlay。失败时保留已建立的 pending owner并返回 gateway error；不得释放给另一 Authority 认领。
5. 调用底层 writer；在此之前没有上游 response bytes 对客户端可见。

写结果分为：

- `WriteHeader` 或 `Flush` 正常返回，或 `Write` 返回 `n > 0`：底层已接受可见边界，continuity 转 `committed`，当前 RouteTarget 固定。
- 底层调用 panic、返回 `n == 0` 且 error，或 adapter 无法证明是否接受了 header/bytes：结果为 `uncertain`。Cookie merge 不回滚，continuity 保留 `pending` owner直到正常 TTL/tombstone 清理；同一值仍只能由原 owner 验证。
- gate 成功后客户端断开也不回滚；传输 ACK 不是 Go `ResponseWriter` 能证明的事务边界。
- `Write` 部分成功 (`n > 0` 且 error) 已可见，按 committed 处理。零长度 Write 不自行定义可见；若底层 adapter 仍可能提交 header，则按 uncertain。

每个 response gate 只执行一次；后续 chunk复用结果。JSON/SSE 中新出现的 response ID 必须在 E0 定义的 chunk/event 边界使用同一“写前 pending、写后 committed/uncertain”规则，原始 bytes 不重写。分属 continuity 和 Cookie 的 SQLite 操作不伪装成跨库原子事务；顺序固定为 pending 后 merge，使任何局部失败都不会释放 owner或泄露未持久化 Cookie。

### 3. Cookie 并发以事务提交顺序形成总序

- 一个 upstream response 内，相同 CookieKey 的多个 `Set-Cookie` 按 wire 顺序处理，最后一个有效操作获胜；Max-Age 优先于 Expires，删除是同 key tombstone。
- 不同逻辑请求提交同一 `JarID + Authority + CookieKey` 时，SQLite 写事务提交顺序是唯一总序，最后提交的 upsert/delete 获胜。不使用 wall-clock `ObservedAt` 决定先后，也不做整 Jar read-modify-write。
- 不同 CookieKey 在同一事务逐 key merge，不丢失并发请求对其他 key 的更新。SQL unique constraint 和事务是正确性来源；进程 mutex/单连接只可优化，不是证明。
- overlay 只在当前逻辑请求内存在；同 CookieScope 的 401/retry 可读，切换 Authority 时不复制，discarded attempt 删除对应 bucket。

### 4. TTL、容量和线缆限制是注入的 typed CookiePolicy

第一版默认值固定如下，全部以具名字段/常量表达并使用 fake clock测试：

| 策略 | 默认值 |
|---|---:|
| `HandleIdleTTL` | 30 days |
| `HandleAbsoluteTTL` | 180 days |
| `HandleRefreshWindow` | 7 days |
| `SessionCookieTTL`（上游无 Max-Age/Expires） | 24 hours |
| `MaxPersistentCookieTTL` | 90 days |
| `OrphanAuthorityGrace` | 24 hours |
| `MaxSetCookieHeadersPerResponse` | 64 |
| `MaxSetCookieBytesPerResponse` | 64 KiB |
| `MaxSetCookieLineBytes` | 8 KiB |
| `MaxCookieNameBytes` | 256 |
| `MaxCookieValueBytes` | 4096 |
| `MaxCookieDomainBytes` | 253 ASCII bytes |
| `MaxCookiePathBytes` | 1024 |
| `MaxOutboundCookieHeaderBytes` | 16 KiB |
| `MaxCookiesPerAuthority` | 180 |
| `MaxAuthoritiesPerJar` | 32 |
| `MaxCookiesPerJar` | 720 |
| `MaxHandleBindingsGlobal` | 10,000 |
| `MaxCookieEntriesGlobal` | 100,000 |

规则：

- Handle 的 idle expiry 在有效使用时更新；剩余时间进入 refresh window 才重发同一 handle Cookie。到 absolute expiry 后必须创建新 handle 和空 Jar，不延长原 Jar。
- persistent Cookie 的实际 expiry 是 upstream expiry 与 `observed_at + MaxPersistentCookieTTL` 较早者；session Cookie 使用 `SessionCookieTTL`。读取不延长 Cookie expiry。
- expiry/delete 立即在 selection 中不可见，并由 cleanup 物理删除。不可达 Authority 经过 grace 后才清理；可达性按 CredentialSession/RouteTarget 计算，不按 ProviderID。
- malformed 单条 Cookie 按具名原因忽略；任何单条/聚合/出站大小上限超出都使当前 boundary 显式失败，不能截断或挑选部分 Cookie。
- merge 前先清除 expired entries。per-Authority/per-Jar 超限时，只在同一 Jar 内按 `LastAccessAt` 最早、再按 creation time、最后按 canonical CookieKey 稳定淘汰，并记录 count/reason；不得跨 ClientScope 淘汰别人的条目。
- global handle/Cookie cap 达到后先清理 expired/orphan；仍超限则新建/merge 返回 typed capacity error，不跨 owner 做全局 LRU。
- out-bound Cookie 排序固定为 path 长度降序、creation time升序、canonical CookieKey升序；超过 Header 上限整体失败，不静默丢弃可能承载会话身份的 Cookie。

Policy 是 startup typed config，可由部署显式覆盖正整数容量/TTL，但 release flag仍只有 ADR-0002 的四个。解析失败或零/负值拒绝启动；运行中的逻辑请求捕获 immutable policy snapshot。

## 拒绝的方案

- 无条件信任 `X-Forwarded-Proto`：任意客户端可伪造 Secure 判定；只有布尔 `trust_proxy_headers` 而没有 peer allowlist也不足以建立信任。
- 在现有 post-write callback 中写 owner/Cookie：Header、Flush 或首字节已经可见，存储失败无法安全返回错误。
- 把网络 ACK 当 committed 条件：标准 ResponseWriter 不提供该证据，会让成功响应长期停留在不确定状态。
- uncertain 时释放 pending或回滚 Cookie：客户端可能已经看见值，随后另一个 Authority 会获得同一 owner。
- 以 wall clock/response arrival 决定并发同 key 胜者：时钟回退和跨 goroutine observation无法形成可靠总序；数据库 commit 已提供明确序列化点。
- 整 Jar 覆盖、随机淘汰、全局跨租户 LRU或出站截断：都会丢失无关并发更新或制造不可观察的身份变化。
- 完全遵从无限 upstream expiry：代理会无限期保留认证材料；明确 retention cap 是本地安全策略。

## 迁移影响

- 新建独立 handle/authority/cookie 表和 schema version；现有客户端 raw Cookie、上游 Set-Cookie 或任何历史 capture 都不导入。首次启用从空 Jar 开始。
- HTTP response wrapper 必须重构为真正的 pre-commit owner；现有 after-write metrics callback 可继续观察，但不能承担安全状态改变。
- gateway-owned Header 与 sanitized upstream Header 显式组合；上游 `Set-Cookie` 永不下发，`switch_a_codex_jar` 在任何 flag 状态都不上游。
- Cookie flag开启时 HTTP 使用请求级 no-auto-redirect；3xx 作为普通 final boundary。WS probe/non-probe 使用同一 overlay规则，但 post-101 失败的 wire error等 E0。
- Policy 字段进入 startup config和验证，不进入四个 runtime release flags；修改 policy只在重启后的新操作生效。

## 可测试不变量

- 只有 direct TLS 或 trusted peer 的一致单值 scheme evidence 会设置 Secure；untrusted spoof header不影响结果，trusted ambiguity为 unknown/fail closed。
- 任一底层可见写之前 pending和Cookie merge已成功；gate failure 时 writer没有上游 response bytes。
- partial Write提交 owner；不确定写保留 pending和已merge Cookie；discarded attempt没有 committed binding或Cookie落库。
- 同 response同 key以wire末项胜出；并发同 key以事务提交末项胜出；并发不同 key都保留。
- API key/JarID/Authority任一不同都无法选择或淘汰另一 scope Cookie。
- fake clock精确覆盖 idle/absolute/refresh/session/persistent/orphan边界；读操作不延长 Cookie expiry。
- 所有容量淘汰可重复且只发生于同 Jar；global exhaustion显式失败；出站超限不产生截断 Header。
- HTTP、WS、`https/wss` 与 `http/ws` 对同 Authority复用一致，同时 Secure Cookie只发往安全 upstream scheme。

## 证据门槛

pre-commit 和 Cookie 事务语义已经固定；对客户端呈现的具体 HTTP status、JSON error code、WS error event/close code仍由 E0 的真实目标 Codex 版本兼容证据决定。尤其不得凭经验给 probe post-101 storage failure选择可能触发无限重连的 `1012`。
