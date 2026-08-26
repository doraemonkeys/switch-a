# Codex 其他协议 Header 执行计划

> 认证 Header 清理和 WebSocket subprotocol 修复可独立实施；会话身份边界与 [Codex 状态 Header 兼容计划](CODEX_WEBSOCKET_COMPATIBILITY_EXECUTION_PLAN.md) 使用同一发布开关。

## 目标

补齐 CLIProxyAPI 中更成熟的 Header 行为：上游认证归属明确、WebSocket subprotocol 正确协商、会话身份不跨上游 Authority。

只处理下表字段及目标 Codex 版本 fixture 已确认的固定 `client_metadata` 投影；不扩展为通用白名单，不硬编码 Codex `User-Agent` 或 `OpenAI-Beta`，不改写请求体，也不解析 opaque Turn Metadata 内部结构。

| 字段 | 规则 |
|---|---|
| `Authorization`、`X-Api-Key` | 删除客户端值和上一次尝试值，只注入当前 Provider 需要的一种认证 Header |
| `ChatGPT-Account-Id` | 不信任客户端值；仅 ChatGPT Provider 注入当前凭据的实际账号 ID，其他 Provider 删除 |
| `Sec-WebSocket-Protocol` | 通过 WebSocket API 协商，不作为普通 Header 复制 |
| `Thread-Id`、`Session-Id`/`session_id`、`Conversation_id`、`X-Codex-Window-Id` | 未知值首次发送前原子认领当前 ClientScope 和 ProtocolScope；同 owner 原样转发，已绑定冲突时删除整 Header |
| `X-Client-Request-Id` | 同一逻辑请求及其重试保持不变，不作为会话身份删除 |

## 实施步骤

### 1. 收紧上游 Header 所有权

在 HTTP 和 WebSocket 的最终请求构造边界以大小写不敏感方式统一清除客户端认证及账号 Header，再调用 Provider 凭据注入。每次重试和 Provider 切换都从干净 Header 开始，不能残留前一次尝试的认证或账号。

保持现有普通协议 Header 透传，不引入 Codex 全量白名单。

### 2. 补齐 WebSocket subprotocol

解析客户端提供的 subprotocol 列表并使用 Dial/Accept 的专用字段传递。

非 probe 路径先让上游选择，再把实际选择值返回下游。probe 路径先与客户端确定一个值，随后只向上游提供该值；上游选择不一致时以协议错误关闭连接，不能静默建立两个协议不同的端点。

### 3. 扩大会话身份边界

复用 `internal/codexidentity` 生成的 ClientScope、ProtocolScope、`internal/codexheaders` 和统一 continuity 存储，不增加第二套持久化。会话身份以“规范化字段类别 + 完整 opaque 值”的 HMAC 为键：未知值在凭据注入和 AppliedIdentity 校验完成后、首次发送上游前按 `pending -> committed` 生命周期原子认领当前 ClientScope 和 ProtocolScope；同 owner 保留，RouteTarget 切换可延续，ClientScope、Authority 或 APIType 冲突时删除整 Header，不重新认领。

Header 名和 `Session-Id` 别名大小写不敏感。

### 4. 校验固定身份投影

只读取源码或版本化 fixture 已确认的 `client_metadata.session_id`、`client_metadata.thread_id`、`client_metadata["x-codex-window-id"]` 等固定路径。Header 与投影表达同一字段时必须完全一致，否则拒绝请求；无冲突时请求体逐字节转发。禁止递归遍历任意 metadata，禁止解析 `client_metadata["x-codex-turn-metadata"]` 内部字段。

## 验证

- HTTP/WS 测试确认客户端认证和账号 Header 不会到达上游，最终请求只含当前 Provider 的认证与账号。
- 重试测试确认凭据刷新、同 Provider 重试和跨 Provider 切换都没有旧 Header 残留。
- WebSocket 测试覆盖无 subprotocol、普通上游选择、probe 一致及 probe 不一致关闭。
- 边界测试覆盖未知会话 Header 并发首次认领、同 Authority 的 RouteTarget 切换、跨 Authority/APIType 删除且不重新认领；`X-Client-Request-Id` 在同一逻辑请求中保持稳定。
- 固定投影测试覆盖 Header/`client_metadata` 一致、冲突、缺失和原始请求体不变；未在 fixture 中出现的路径保持 opaque。
- 最终运行 `make ci`，保持 Go 覆盖率不低于 90%。

## 完成标准

客户端认证或账号信息不会被误发给上游；HTTP 与 WebSocket 只携带当前 RouteTarget 的凭据；WebSocket 两端使用同一 subprotocol；已确认载体中的会话身份不跨 ClientScope、Authority 或 APIType。
