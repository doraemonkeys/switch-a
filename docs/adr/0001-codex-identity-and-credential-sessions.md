# ADR-0001：Codex 客户端隔离、CredentialSession 与 Origin 规范

- 状态：接受
- 日期：2026-08-26
- 影响任务：M1、I1、A1、SEL1、CH2、CC2、PC2

## 背景

当前 `Provider` 同时拥有路由属性、endpoint 和 secret，`ProviderCredential`/刷新状态又以 ProviderID 为生命周期边界。该模型无法表达“多个 RouteTarget 共享一次登录”或“同一账号的两次独立登录”，也无法为 continuity 和 Cookie 提供稳定的上游安全主体。

公共代理端口并不认证调用者。因此，客户端提供的 API key 只能作为持有 secret 的隔离键，不能被描述成已认证的用户、设备或账号。

## 决策

### 1. ClientScope 只来自唯一、无歧义的原始客户端 secret

提取发生在任何 Header 清理、Provider 选择或请求体改写之前。提取器返回 `absent`、`single`、`invalid` 或 `ambiguous` 四种 typed 结果：

- `Authorization` 只接受恰好一个 Header value，格式为大小写不敏感的 `Bearer` scheme、至少一个 SP/HTAB 和非空 token。只移除 scheme 分隔处及 token 两端的 OWS，不改变 token 内部字节。
- `X-Api-Key` 只接受恰好一个 Header value；移除两端 OWS，内部字节不变。
- 单个 token 上限为具名常量 `MaxClientCredentialBytes = 8192`；空值、超限、非法 Bearer 或任一来源的多 value 都是 `invalid`。
- 两种 Header 同时存在时，只有解析后的 token 字节完全相同才得到 `single`；比较使用常量时间。不同值是 `ambiguous`。Header 来源不进入 ClientScope，因此同一 secret 从 Bearer 切换到 `X-Api-Key` 不会意外换 ClientScope。
- `ClientScope` 是 `HMAC-SHA-256(client-scope/v1, token)` 的完整 digest 加 key version；不保存原 token。稳定编码和密钥规则见 ADR-0002。

作用规则：

- Cookie Jar 开启时，每个适用请求都必须是 `single`；`absent`、`invalid`、`ambiguous` 均在任何上游字节前失败。
- Continuity 开启时，只要请求 Header、已确认的固定 body projection 或 WS frame 携带会话 identity、Turn State、Turn Metadata、Attestation、`previous_response_id` 或 `response.inject.response_id`，就必须是 `single`。WS 中较晚出现证据时，在对应 frame 发往上游前失败。
- 没有上述证据的无 key 请求保持 stateless passthrough，不产生 durable owner。它之后若尝试使用先前看到但未绑定的 opaque state/ref，按 unknown 拒绝；禁止把所有无 key 请求放进共享匿名 scope。
- 只有 identity/continuity/Cookie 需要 ClientScope 时才消费该提取结果；Header hygiene 独立开启时不把双 Header 歧义扩张成新的调用方认证策略。

### 2. 每个可独立轮换的 secret 都是一个 CredentialSession

领域模型固定为：

- `RouteTarget`：endpoint、Vendor、APIType 能力、健康、并发和路由策略；现有 ProviderID 可继续作为 RouteTarget 的外部 ID。
- `CredentialSession`：稳定随机 SessionID、Vendor/credential kind、secret、credential version、refresh/auth lifecycle 和 CredentialSubject。
- 显式 `RouteTarget + APIType -> CredentialSession` 引用；RouteTarget 不再存 secret。

映射与迁移规则：

1. Provider 级静态 key 迁为一个默认 static CredentialSession；没有 override 的 APIType 引用它。
2. 每个非空 per-API-type override 各迁为独立 static CredentialSession，并由对应 `RouteTarget + APIType` 显式引用。即使两个旧字段当前字节相同，也不按内容自动合并，因为旧数据无法证明它们应共享未来轮换。
3. 每个旧 ChatGPT ProviderCredential/AuthState 迁为独立 login CredentialSession。相同 account ID 的旧记录不自动合并；共享只能在新模型中通过显式引用表达。
4. 迁移完成后删除 Provider/APIType 行中的 secret、`BindingAccountID` 唯一约束以及 Reject/Replace another provider 语义；不得双写旧字段。
5. refresh singleflight、mutation lock 和 CAS 以 `SessionID + credential version` 为边界。删除 RouteTarget 只删除引用；仅无引用 session 才可进入清理。

CredentialSubject 规则：

- ChatGPT 使用凭据应用后验证得到的实际 account ID；值视为 provider 给出的 opaque canonical ID，不自行大小写折叠。
- 有可信、稳定且由认证结果证明的 provider subject 时使用该 subject。
- 否则 static subject 为 `HMAC-SHA-256(credential-subject/v1, canonical(Vendor, credential kind, secret))`。它在 Session 创建或 secret 变更时计算并持久保存 digest + key version；认证时用该版本重新证明。仅切换 keyring current 不重算 subject，因此不会让既有 Session 换 Authority；无法证明主体不变的 static secret 轮换才产生新 Authority，且不做 Cookie/continuity 继承。
- 相同 CredentialSubject 不等于相同 CredentialSession；独立 Session 的 secret、version 和刷新状态永不共享。

选择前的 resolver 只消费一次性预加载的 RouteTarget/APIType/Session/AuthState snapshot。认证注入必须返回 `AppliedIdentity`；它与预期 Authority 不一致时，在发送任何 state 或 server-side Cookie 前失败。

### 3. Origin 使用一套 IDNA/端口规范；public suffix 只约束 Cookie Domain

`NormalizedOrigin` 的稳定形态是 `(http|https, canonical host, optional non-default port)`：

- runtime Authority 必须从该物理 attempt 已完成拼接的最终 request/dial URL 提取，不能从 ProviderID、base URL 字符串或请求 Header 猜测。
- `ws -> http`、`wss -> https`；scheme 和 DNS host 使用 ASCII 小写。
- DNS host 通过 `golang.org/x/net/idna` 的 Lookup profile 转为 ASCII，移除一个终止 root dot后再规范化；转换失败、空 host 或产生空 label 时拒绝。
- IPv4/IPv6 使用 `net/netip` 规范文本；IPv6 序列化时加方括号；拒绝 IPv6 zone identifier。
- 端口必须是十进制 `1..65535`；`http:80`、`https:443` 折叠，其他端口以无前导零十进制保留。
- runtime URL 允许正常 path/query，因为它们属于实际请求，但只抽取 origin；userinfo 或 fragment 一律拒绝。用于配置、存储和解码的 origin-only 表示只允许空 path 或 `/`，并拒绝 query/fragment，防止把非 origin 字节混入稳定编码。
- `UpstreamAuthority = Vendor + NormalizedOrigin + CredentialSubject`；`ProtocolScope = UpstreamAuthority + APIType`。所有字段使用版本化、长度前缀编码，不用分隔符拼接。

Cookie host 和 Domain 属性复用同一个 IDNA/DNS canonicalizer。Domain 属性去掉前导点后必须 domain-match 实际响应 host，并通过固定依赖版本的 `x/net/publicsuffix` 检查；当 Domain 等于 public suffix 时拒绝，包含 ICANN 与 private suffix。IP literal 只允许 host-only Cookie，任何 Domain 属性都拒绝。Public suffix 不参与 Origin/Authority 判定，因此 localhost、IP 和内部 DNS origin 仍可作为彼此隔离的 Authority。

## 拒绝的方案

- 以 IP、User、installation ID、窗口 ID 或空字符串代替客户端 key：这些值不是稳定且唯一的 secret，会合并不同客户端。
- 双认证 Header 固定选一个优先级：HTTP/WS 或中间代理重排后会得到不同 owner；相同 secret 可合一，冲突必须显式失败。
- 每个 Provider 一个 CredentialSession：继续保留了错误的路由=凭据所有权。
- 按 secret 相等自动合并迁移记录：相等快照不能证明共同轮换和刷新意图。
- 让一个 session 内藏 per-API secret map：AuthorityResolver 仍无法得到唯一、版本化的认证会话。
- 用 ProviderID、vendor failover scope 或配置 base URL 作为 Authority：它们不能证明实际 origin 和实际凭据主体。
- Unicode host 仅做 lowercase，或 Cookie Domain 仅做字符串 suffix：会在 HTTP/WS/SQLite 路径产生不同 canonical value，并允许公共托管后缀跨租户 Cookie。

## 迁移影响

- M1 必须是显式、幂等、事务化 migration，并在真实旧库 fixture 上覆盖默认 key、per-API override、ChatGPT、历史重复 account 和重跑；不能只依赖 AutoMigrate。
- admin CRUD/import/export/auth export、Provider 删除、refresh cache、selector snapshot 和所有 auth mocks 一次迁到 Session 引用；旧 secret 字段和账号独占分支直接删除。
- static secret 轮换若没有可信 provider subject，会自然创建新 Authority；旧 continuity 变 unknown，旧 Cookie Authority 进入可达性/TTL 清理，而不是被复制。
- IDNA 或 origin codec 版本是持久格式的一部分；未来改变规则必须新增 codec 版本和显式迁移，不能静默重算既有 owner。

## 可测试不变量

- 两个受支持 Header 携带相同 token 得到同一 ClientScope；冲突 token、重复 value、空值和超限均不得产生 digest。
- Cookie 请求无 `single` client key 时没有上游字节；无状态、无 key 请求不创建 owner。
- 默认 static key、每个 override 和每条旧 login 记录得到确定数量的 Session；迁移不按 secret/account 自动合并。
- 多 RouteTarget 可显式共享一个 Session；同 subject 的两个 Session 不共享 refresh、secret 或 version。
- static secret 改变时 subject/Authority 改变；ChatGPT refresh 只有 account subject 不变时可保留 Authority。
- `https://EXAMPLE.com:443` 与 `wss://example.com` 相等；`http/ws` 同理；非默认端口、Vendor、subject 或 APIType 变化产生预期的不同 Authority/ProtocolScope。
- Unicode/ASCII IDNA 等价、IPv6 canonicalization、userinfo/zone/非法端口拒绝在 HTTP 与 WS 完全一致。
- `Domain=github.io` 等 public/private suffix 被拒绝；IP Domain Cookie 被拒绝；host-only Cookie 不受该误判影响。

## 证据门槛

本 ADR 不决定 Codex 私有 body/frame 字段路径。哪些输入算“携带证据”只能来自 E0 固定目标版本的源码与 exact-byte fixture；未被 fixture 证明的 projection/event 保持 opaque，不得因本 ADR 扩大解析范围。
