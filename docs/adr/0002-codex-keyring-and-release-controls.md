# ADR-0002：Codex 持久密钥与四个发布开关

- 状态：接受
- 日期：2026-08-26
- 影响任务：KR1、CC2、PC2、CFG2、W3-HTTP、W3-WS

## 背景

ClientScope、opaque owner 和 Jar handle 需要重启后稳定的 keyed digest；Cookie value 需要数据库之外的加密密钥。把随机 key 放在进程内会让重启破坏所有权，把 key 放进同一 SQLite 又不能抵抗数据库泄露。运行时开关与密钥能力也必须形成一个原子、可验证的启用契约。

## 决策

### 1. 启动时加载两个版本化 keyring

启动配置只增加非秘密路径 `codex_keyring_file`，环境变量名固定为 `SWITCHA_CODEX_KEYRING_FILE`。目标文件由部署 secret mount/受保护文件提供，不进入 SQLite `RuntimeConfig`、admin API、配置导入导出或日志。

文件是 UTF-8 JSON，schema 固定为：

```json
{
  "schema_version": 1,
  "hmac": {"current": "h2", "keys": {"h2": "<base64url>", "h1": "<base64url>"}},
  "aead": {"current": "a2", "keys": {"a2": "<base64url>", "a1": "<base64url>"}}
}
```

- key ID 匹配 `[A-Za-z0-9._-]{1,32}`，每个 material 是无 padding base64url 编码的恰好 32 bytes。
- HMAC 与 AEAD root 必须不同；重复 ID、重复 material、缺 current 或未知 schema 都使 keyring 无效。
- keyring 在 composition root 构造一次并以窄接口注入；模块不得自行读取 env/file，也不得运行时静默重载。
- HMAC 使用 HMAC-SHA-256。HMAC root 经 HKDF-SHA-256 和固定 context 派生 `client-scope/v1`、`credential-subject/v1`、`opaque-binding/v1`、`jar-handle/v1` 子 key。
- Cookie value 使用由 AEAD root 派生的 AES-256-GCM key和每次 `crypto/rand` 生成的 96-bit nonce。AAD 使用版本化长度前缀二进制编码，至少包含 codec/purpose、key version、JarID、Authority 全字段与 CookieKey 全字段。
- HMAC digest 保存完整 32 bytes 和 key version；Cookie 行保存 ciphertext、nonce 和 AEAD key version。缺 version、nonce 失败、认证失败或未知 key 都返回 typed error，不能当作 unknown/expired/空 Jar。

### 2. current 只签发，legacy 只读取

- 新 digest/密文只使用对应 ring 的 current key。
- 非 current key 只用于验证/查找/解密；传入 opaque 值时可针对受支持的 legacy HMAC versions 计算候选 digest，版本数由 keyring 文件显式限制。
- 成功读取 legacy Cookie 后，在同一后续成功 merge 事务中用 current AEAD key 重写；只读请求不因重加密额外改变业务状态。
- versioned HMAC identifier 不因 current 切换而原地重写。验证输入时计算 current + legacy 候选；旧 binding 等 TTL 清理，static CredentialSubject 按 ADR-0001 保留创建它的版本直到 secret 变更或 Session 删除。这样 key rotation 不改变既有 owner；删除 legacy 前的数据库预检必须确认已无任何 binding、ClientScope 或 Session subject 引用。
- 轮换流程是“文件同时部署 old+new并把 new 设 current → 重启 → 观察 legacy 使用并完成重写/保留期 → 确认数据库不再引用 old → 删除 old → 再重启”。不得先删除仍被行引用的 key。
- 恢复只接受恢复原 keyring 备份；不自动生成替代 key、不删除无法解密的行、不把数据库清空后继续。丢失引用中的 key 是显式运维故障。

### 3. feature enablement 与 capability 原子校验

四个 RuntimeConfig key 精确为：

1. `codex_upstream_header_hygiene_enabled`
2. `codex_websocket_subprotocol_enabled`
3. `codex_continuity_enabled`
4. `codex_provider_cookie_jar_enabled`

全部默认 `false`。语义边界：

- header hygiene 只控制 attempt 级 auth/account 清理与当前凭据投影；WS subprotocol 只控制专用协商。二者可独立启用。
- continuity 同时控制三类状态、response ref、四类会话 identity Header 和 E0 确认的固定 body/frame projection；不允许把 session identity 与 state 分成半启用状态。
- Cookie Jar 独立于 continuity；它共享 identity/AppliedIdentity，但不共享 owner store 或 enablement。
- `continuity=true` 或 `provider_cookie_jar=true` 要求 `upstream_header_hygiene=true`。管理写入违反依赖时拒绝；不能通过关闭 hygiene 留下仍启用的依赖项。WS subprotocol 不作为另三项的隐式依赖。
- switch 自有 Jar handle 在所有请求上始终被剥离，不受 Cookie flag 影响；关闭 Cookie 不能把 gateway handle 变成上游 Cookie。

启动时先读取并验证完整 typed feature snapshot。若持久配置启用了功能而 schema、migration、protocol catalog、identity/auth capability 或所需 current/legacy key 不完整，进程不得开放监听端口。全部相关功能关闭时允许没有 keyring，从而不阻断既有部署。

运行时配置更新必须先做组合与 capability validation，再持久化并原子发布不可变 snapshot。每个 HTTP 逻辑请求和 WS session 只捕获一次 snapshot；中途切换只影响新操作。配置读取/刷新失败保留最后一个已验证 snapshot并记录结构化故障，绝不能临时退回默认 `false`。已启用功能的 repository/keyring/runtime failure 对该请求 fail closed。

发布顺序固定为 hygiene → WS subprotocol → continuity+session identity → Cookie Jar。schema migration 是前向变化，关闭开关不恢复旧 credential/schema 或双写路径。

## 拒绝的方案

- 进程启动时随机生成 key：重启后所有持久 owner 和 Cookie 都不可验证。
- 把 raw key 或加密 key 存进 RuntimeConfig/同一 SQLite：admin 导出或单次数据库泄露会同时获得数据和保护材料。
- 所有用途共用一个未派生 HMAC key，或 HMAC/AEAD 共用 root：扩大跨协议攻击与误用半径。
- 读失败时当作空 Jar、unknown state 或 feature=false：故障会把安全隔离静默降级。
- runtime 热重载 secret：并发请求会看到不同 keyring；轮换通过完整文件和进程重启形成清晰代际。
- continuity identity/state 拆成多个开关：会产生只校验一种载体、另一种仍穿透的不可推理组合。

## 迁移影响

- KR1 新增 startup config 与 keyring 深模块；只有 key version/密文进入业务表。
- M2/M3 migration 必须记录所有引用版本，并提供“数据库所需 versions”预检；未来 schema/version 不能回落读取。
- CFG2 用一个后端 typed registry生成默认值/验证规则，并用 contract test约束前端 key 集合；不再由多处字符串常量各自定义语义。
- 首次部署先迁移 schema并保持四 flag 为 false。启用 continuity/Cookie 前必须备份 keyring；数据库备份而没有匹配 keyring 不构成可恢复备份。

## 可测试不变量

- 相同 root/version/purpose/input 跨重启产生相同 digest；不同 purpose、version 或 ring 不同。
- 修改 AAD 任一 Jar/Authority/CookieKey 字段、nonce 或 ciphertext 都无法解密，且不会覆盖原行。
- current 只产生新写；legacy 可读不可签发；仍有引用时删除 legacy 会使启动/启用失败。
- feature 全关闭且无 keyring 可启动；持久 flag 已开启但 capability 缺失时监听器不启动；管理端也不能写入不可满足的组合。
- 16 种 flag 输入组合都有 contract test；违反 hygiene 依赖的组合被拒绝，Cookie 与 continuity 的两个合法方向都可独立运行。
- 一个进行中的 HTTP/WS 操作只观察一个 immutable snapshot；配置故障不改变为更宽松语义。
- RuntimeConfig、admin export、日志和 SQLite 均不含 root key或原始 secret。

## 证据门槛

`codex_continuity_enabled` 在 E0 没有固定目标 Codex 版本、支持字段 catalog 与客户端接受的 HTTP/WS 错误契约前不得启用。Cookie flag覆盖 WS 时，post-101 storage failure 的 close/event 合约同样必须由 E0 真实客户端证据确定；本 ADR 不猜 HTTP status、WS event 或 close code。
