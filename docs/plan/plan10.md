# 请求入口与重放重构计划

## 背景与目标

统一处理以下问题，保留现有路由、连续性、重试和透明转发行为：

| 现有问题 | 目标 |
|---|---|
| HTTP 全量预缓冲阻止上传流水线，并放大并发内存占用 | 用可增长的重放存储和流式事实投影降低内存；选路依赖满足后即可上传。 |
| WebSocket replay 与 selection probe 共用 128 消息上限 | 按实际保留成本管理 replay，独立限制探测工作量；预算耗尽仍继续 live 转发。 |
| 重建 HTTP 请求改变未知长度并丢失 trailer | 分开保存客户端声明与上游 framing，随 attempt、body 重开和 redirect 正确投影。 |

已有采样包含 55 条 HTTP、9 条 WebSocket，HTTP 未记录未知长度或非空 trailer。但当前 capture 取自重建后的上游请求，不能据此推断客户端原始特征。入口在读取 body 前记录协议、声明长度、`TransferEncoding` 和 trailer 声明，EOF 后补实际接收量和完整 trailer；通过 operation/attempt ID 关联上游记录。

HTTP 使用一条入口管线：逻辑请求拥有原始输入，attempt 拥有自己的 reader，领域消费者决定何时可以选路。小请求优先用内存，大请求按需落盘；需要完整语义的 Codex 请求仍等待 EOF，主要收益是降低内存，没有阻塞型 body 依赖的请求可以提前上传。

## HTTP 设计

### 事实就绪

仅为现有消费者表达依赖：

- `BeforeSelection`：实际生效的 model 路由、model sticky 和 Codex continuity。每次选路使用与该次决策一致的配置和事实；已有 URL/header 事实足够时不等待 body。
- `ObservationOnly`：日志、摘要和统计，不阻塞转发；进行中显示 pending，结束时补事实或不可用原因。

当前 error rule 的 `RequestScope` 只有 Provider 和 API 类型，无需增加 body-facts 接口。是否开始上传由实际依赖决定，不根据 API 名称、请求大小或 `MaxRetries` 推测。

事实区分 `pending / known / unavailable`，unavailable 保留字段缺失、解码不支持、格式错误或 decoded limit 等原因。pending 的阻塞依赖继续等待；获取结束后由消费者沿用原有接受、降级或拒绝策略，保持观察性解码失败后转发原始请求及现有 continuity 拒绝条件。

### 流式语义投影

- content coding 按逆序用 reader 解码，保留现有 decoded-size 上限，只产出 model、reasoning 和 Codex client evidence 等事实，不物化完整 decoded body。
- 大型无关字段按流跳过，包括字符串；避免使用会完整保留字段的 `RawMessage` 或 token API。
- 各消费者保留原有重复字段、大小写和尾随 JSON 规则；首次读到 model 不代表最终事实。需要完整判定时读取至 EOF，处理解压尾部错误后再发布结果。
- Codex runtime 接收投影后的 evidence，移除 HTTP 路径对完整 wire/semantic byte slice 的依赖。语义不可用只结束对应事实获取，不停止仍健康的 wire source。
- 迁移时用相同输入对比新旧消费者的结果，包括重复字段、大小写、转义、尾随内容、巨大字符串和解压尾部错误。旧解析逻辑可作为测试参照，生产路径统一使用新投影。

### Request ingress

新增 `internal/requestingress` 深模块，返回具体 handle；transport 在消费侧定义最小 body-source 接口。context 通过调用传入，不存入 struct。

- ingestion pump 单独顺序读取客户端 Body，将原始 wire bytes 追加到 spool。内存 segment 同时受单请求阈值和进程共享额度控制；达到阈值或申请不到额度时，新 segment 直接落盘，不搬移正在被 reader 引用的内存。segment 释放时归还额度。
- 健康输入的读取节奏受客户端、spool 写入速度和现有 `max_body_size` 限制，不随某次上游读取暂停；上游慢时允许磁盘积压并记录规模。共享额度只控制 spool 内存，解压器、必要 facts 和 capture 分别核算。
- 每个 attempt 从 offset 0 打开 reader，到达写入前沿时等待新数据或源终态。已接收前缀保留至重试窗口关闭且相关消费者释放。
- 源状态分为 `receiving / complete / failed / aborted`：健康上传、完整输入、读取/超限/存储故障、网关主动停止。attempt 的打开、关闭和读取进度独立记录。
- retryable early failure 保留 ingestion；先取消并关闭旧 reader、等待其读取退出，再让下一 attempt 重放前缀并跟随 live tail，继续沿用重试、disclosure 和 continuity 决策。
- attempt Body.Close 仅结束本次读取。源终态唤醒等待者，EOF 不删除 spool；`ingress.Close` 禁止新 reader 并启动收尾，待 pump、reader 和存储引用释放后统一清理 segment 与临时文件，清理只执行一次。

阈值和共享额度使用命名内部常量，依据本地请求分布与内存、磁盘测量调整，暂不增加面向用户的存储配置。

### 提前响应、取消与超时

- 2xx 响应头不代表最终成功，继续现有 HTTP/SSE 分类和 pre-commit 错误检测；响应可见性与输入完整性分别记录。
- 重试窗口关闭仅解除重放需求。活动 attempt 仍在消费时继续上传；上游已停止消费且不再需要重试，或整个操作终止时，主动停止剩余输入，不等长 SSE 结束。
- HTTP/1 并行读取请求和输出响应前，通过 `ResponseController.EnableFullDuplex` 启用双工，ResponseWriter 包装透传相关能力；保持 `Expect: 100-continue` 及其他 header policy。
- 输入中断路径实际解除阻塞 Read。HTTP/1 读 deadline 的错误可能取消 `r.Context()`，操作 context 因此独立管理：登记主动停止原因，再中断读取、关闭 Body 并等待 pump 退出，保留最终响应转发。
- 主动停止后保留独立的连接/流关闭检测，区分主动读中断与真实客户端断开；真实断开即取消上游并释放并发租约，不依赖后续响应写入或非零超时。主动停止标记 aborted，capture 保留部分输入与原因。
- 分开记录接收上传、等待上游响应头和读取响应的时间。现有 `FirstByteTimeout` 映射到 `ResponseHeaderTimeout`，从请求体写完后开始计时，流式化不悄悄改成从选路或发出请求头计时；客户端上传等待不计作 Provider 响应超时。

### Framing

入站声明长度用于校验和记录，上游 framing 按目标协议生成；spool 已保留多少字节不决定声明长度。读取前冻结协议、`ContentLength`、`TransferEncoding` 和 trailer 声明，读取期间不并发访问原始 trailer map，EOF 后冻结完整 key/value 集合。

- 通常保留已知/未知长度语义，长度不一致属于源故障；未知长度不因 spool 收满而改成已知长度。零长度且无 body 时使用 `http.NoBody`。
- HTTP/2 可同时具有确定长度和 trailer；转为 HTTP/1.1 时，为传递 trailer 使用 chunked framing，入站声明长度仍用于输入校验。HTTP/2 输入尚未结束且 trailer 集合未定时，HTTP/1.1 上游也需预留 chunked framing，才能保留后到的未声明 trailer；输入已完整且无 trailer 时可保留声明长度。
- 每次发送创建独立的非 nil trailer map，填入当时已知的 key；reader 返回最终 EOF 前向同一 map 补齐完整集合，包括未预声明字段。不同 reader 不共享可变 map，EOF 后不再修改。
- redirect 保持 `FollowRedirects / ExposeRedirects` 策略。301/302/303 丢弃 body 时同时清除对应 framing；307/308 从同一 ingress 重开 reader，并重建配套长度和 trailer。
- transport 负责每一跳的 body/framing 配套构建。`GetBody` 单独返回 reader 不足以处理 trailer：Go redirect 不会同步复制它，Transport 内部重开也不能假定得到独立 map。两条路径均纳入 reader 生命周期，保持现有重试资格和凭据转发策略。
- 普通 Header 中不直接复制 `Transfer-Encoding` 或 `Trailer`；协议转换不保留 chunk 边界、Header 顺序和大小写。

### 失败语义

- 声明长度超限时在连接 Provider 前返回 413。
- 读取中超限时停止 ingress 和当前 attempt，禁止重试；下行未提交时返回 413，已提交时终止响应并记录原因，已发送的前缀无法撤回。
- 客户端 EOF 前中断使源进入 failed，残缺 spool 不再重试；健康的 receiving 源仍可重试。
- spool 故障归因于 Gateway ingress，客户端中断、主动停止和源故障引起的上游取消不计为 Provider 健康失败。
- 解码或投影失败由事实消费者处理，不混入 wire-source 故障。

### 集成与可观测性

- `proxyContext.body []byte` 替换为 ingress handle，同步迁移 `ConsumeAndReplaceBody`、全量 decoder 和 `BuildRequest(..., body []byte)` 的调用者；错误摘要保留固定长度前缀。
- capture 增量记录入口 head/framing 和逻辑 body，attempt 引用同一逻辑 body。按现有 capture 预算保留或截断证据，不阻塞上传或关闭 replay；引用释放前保持存储有效。
- HTTP 计数改为 `UpstreamBodyReadBytes`（`upstream_body_read_bytes`），记录各次上游读取 body 的量，包括 retry/redirect 重读，同步更新字段和 UI 文案。它不表示线上发送量或上游已收到量，不再预加 `len(body)`。
- ingress 接收量与 attempt 读取量分别统计；disclosure 继续取自 `RequestDisclosure`，不能根据读取量为零允许切换 Provider。
- 在 started、admission-ready、spilled、completed、failed、aborted 和 attempt-opened/closed 等转换记录 trace，包含 operation/attempt ID、事实与源状态、原因、接收量、保留量、disclosure 和 response-committed。
- 观测 active ingress、admission wait、spool 内存/磁盘保留量、共享内存额度、decoded bytes 和 capture 保留量，避免其他层重新物化完整 body。

## WebSocket 设计

- replay budget 计入 payload capacity 和 descriptor backing array capacity；共享 payload 只计一次，snapshot 额外保留的 descriptor 同样计入。默认预算按新口径校准，保留原有不超过 128 条、累计 payload 不超过 4 MiB 的可重放输入及其 snapshot 所需空间。
- 移除固定消息数门槛。超过 128 个可重放小消息且保留成本未超预算时仍可重放；空消息也计算 descriptor 成本，保持原有消息资格、顺序、类型、边界和 lineage。
- selection probe 独立使用 duration、decoded bytes 和 work-unit 预算，不复用 replay 常量。预读帧由待首次投递队列持有，选路后按原序投递；关闭 replay 不丢弃该队列或关闭健康连接。
- payload 写入后不可变；snapshot 只复制 descriptor 并持有 payload 引用，状态查询不复制消息。
- replay 状态区分 replayable、visibility closed、budget exhausted、non-replayable frame 和 parse degraded，记录 session/operation ID、消息数、保留量及原因。
- 预算耗尽继续 live 转发，但不丢弃旧前缀后尝试部分重放；后续故障保留 replay-unavailable 原因。覆盖时长依据实际消息时序观察，不以音频间隔推算。

## 实施顺序

1. 独立修复 WS 预算耦合、snapshot 深拷贝和诊断状态。
2. 补入口事实记录和现有消费者的解析样例；用本地连接跑通 HTTP 双工、主动停止后继续响应及随后断开，以及跨协议 framing、redirect 和 Transport body 重开，再接入 Handler。
3. 实现 spool、共享内存额度和流式投影，迁移 body 的 `[]byte` 边界与 capture；此时可仍读至 EOF 后转发，先消除完整请求体常驻内存。
4. 接通 retry、超时归属、统计和生命周期，为 `BeforeSelection` 已满足的请求启用流式 attempt，包括上传中的 replacement。
5. 清除旧 helper 与兼容分支，结合 benchmark 检查内存、磁盘和清理行为，运行 `make ci`，保持 Go 覆盖率不低于 90%。

## 本地开发

复用现有 [HTTP 断开测试](../../internal/proxy/handler_client_disconnect_integration_test.go) 和 [WS 刷新重试测试](../../internal/websocketproxy/gateway_integration_test.go)：测试客户端、网关和本地上游通过真实 socket 通信。使用 `httptest` 启动 HTTP/1.1、HTTP/2 和 WS 服务，raw TCP 补充精确 framing 场景；认证刷新使用可控实现和测试 token，不需要真实账号或外网服务。

- 本地上游按脚本慢读、提前返回 401/2xx、发送 SSE 错误、重定向或断开；客户端用 channel/barrier 控制上传、暂停和取消，观察完整链路的字节、trailer、选路、失败归属与资源释放。协议场景走真实 transport，避免把待检查的行为本身 mock 掉。
- 重点组合包括上传未结束时 replacement、主动停止上传后继续 SSE 再断开、默认零超时下主动停止上传后上游静默且客户端断开、HTTP/2 带长度和后到 trailer 转为 HTTP/1.1，以及 WS 多次重放中继续收到消息。断开场景验证上游取消和租约释放；WS 分别验证 replay 耗尽后仍可首次投递、原有预算范围内仍可重放。随实现补充场景，不依赖 `time.Sleep` 排时序。
- 状态机、投影和预算用小阈值、可控时钟及文件读写故障注入；benchmark 比较压缩体、巨大字段、并发、capture 开关及存储清理。内存额度充足的小请求留在内存，额度不足时落盘；耗时数字不作为默认单测门槛。
- 已有请求样本可作为本地 fixture，缺少的协议形态直接构造。结果说明网关在这些输入和时序下的行为，平台实际账号策略与风控不能由本地模拟推断，也不作为此次开发的前置条件。

全程保持客户端 body 原文及既有 header policy，不增加客户端未发送的 Cookie、不改动 Accept-Encoding、不引入上游 magic strings。
