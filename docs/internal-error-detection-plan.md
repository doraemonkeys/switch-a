# 内部错误检测实施方案

> 状态：实施定案（2026-08-03）  
> 范围：HTTP 与 SSE；WebSocket、正则表达式和合成客户端错误留到后续版本。

## 原始需求

用户原始描述：

> 支持全局或者账户级内部错误检测，关键词匹配。比如 codex 设置内部错误检测到 "Our servers are currently overloaded at capacity" 就能自动进入重试和切换逻辑（可配置只重试不切换，可配置这种错误的重试次数，可配置不重试直接透传客户端）。
>
> 你看看怎么实现 UI 和交互达到最佳用户体验。

## 1. 背景与目标

上游可能在 HTTP 200 响应中返回应用层错误。例如 Codex SSE 流先发送
`response.created` / `response.in_progress`，随后发送
`server_is_overloaded` error。当前代理只按传输错误和 HTTP 状态重试，因此会把该响应记为成功并直接透传。

本功能需要：

1. 从受支持协议的错误对象中匹配关键词，不扫描正常输出内容。
2. 支持全局规则和 provider 规则。
3. 命中后执行：同 provider 重试后切换、仅同 provider 重试、直接透传。
4. 在客户端看到内容前可吸收错误；提交响应后只记录，不能重试。
5. 每次判断都可从请求尝试记录和结构化日志中还原。

## 2. 领域模型

配置与运行统计分离：

```text
InternalErrorRule
├─ id          string
├─ name        string
├─ enabled     bool
├─ target      global | provider(provider_id)
├─ api_type    string?             # 请求路由契约；空表示所有内置 API type
├─ keywords    string[]
├─ match_mode  any | all
├─ action      RuleAction
├─ position    int64               # 显式、可调整的规则顺序
├─ created_at  time
└─ updated_at  time

RuleAction
├─ passthrough
├─ retry_only(max_retries, backoff)
└─ retry_then_switch(max_retries, backoff)

InternalErrorRuleStats
├─ rule_id      string
├─ hit_count    int64
└─ last_hit_at  time?
```

`RuleAction` 是判别联合，不保存与当前动作无关的重试字段。v1 不提供
`exhaustion_behavior`：`retry_only` 耗尽后直接提交当前仍保持打开的上游错误响应。

持久层必须保证：

- global target 不得有 `provider_id`；provider target 必须引用现存 provider；
- provider 删除时级联删除其规则；
- `max_retries` 为 0–10，表示由该规则触发的额外尝试次数；
- `backoff` 完整复用 `BackoffPolicy` 及其校验：`initial_delay=0` 表示不等待，负数非法；`multiplier=0` 使用默认值，`=1` 为固定间隔，`>1` 为渐进间隔；`max_delay` 和 `jitter` 语义不变；
- 关键词保存前 trim、大小写归一化、去重，并限制规则数、每条关键词数及关键词长度；
- enabled 规则只能选择已注册的内置 API type。`custom:*` v1 不允许启用内部错误规则。

规则中的 `api_type` 始终指请求路由解析出的 API 契约。响应分析器另行产生
`response_protocol_id`；它是运行时事实，不参与规则 scope，避免把 Codex、
OpenAI-compatible 等请求契约与 SSE/JSON 响应格式混为一谈。

## 3. 规则解析

代理在逻辑请求开始时取得一个不可变、带持久化 revision 的规则快照；同一请求的所有重试始终使用该快照。

候选规则按以下顺序比较：

1. provider 规则优先于 global；
2. 精确 `api_type` 优先于 All；
3. `position` 升序；
4. `id` 仅作为确定性兜底。

匹配器只检查协议提取器返回的 `type`、`code`、`message`、`reason` 等字符串语义字段；字段分别匹配，不先拼接。关键词和字段值统一 trim、转小写后做子串匹配，空关键词非法。`all` 表示每个关键词至少命中一个字段。对同一错误对象，第一个匹配规则生效；响应在提交前首次命中时立即结束探测并进入决策。试匹配接口同时返回全部匹配规则和最终规则。

## 4. 运行时架构

```text
RuleSetProvider
      ↓
ResponseAnalyzer → PendingResponse + AttemptObservation
                              ↓
                    RetryDecisionEngine
                              ↓
             commit | retry_same | switch_provider
                              ↓
                    HealthAssessor + Evidence
```

### 4.1 响应所有权

`ResponseAnalyzer` 返回的 `PendingResponse` 持有响应元数据、原始字节前缀及内部 pump/coordinator，但不暴露 response body。pump 是上游 body 的唯一 reader，coordinator 是客户端 response writer 的唯一 writer，并按有界通道收到的顺序处理原始字节和控制事件。

`PendingResponse` 只有 `probing → forwarding` 和 `probing → discarded` 两个合法终结转换，转换由 coordinator 线性化且只能成功一次：

- `Commit`：coordinator 写一次响应头和已缓冲原始前缀，确认转换后继续按序转发 pump 产生的后续字节；executor 不读取 body；
- `Discard`：coordinator 关闭 body 以中断 pump，等待 pump 退出，不向客户端写任何内容。

执行顺序固定为“分析 → 决策 → Commit/Discard”。不得在重试决策前关闭响应，也不得把 `PendingResponse` 或 live body 塞进仅承载事实的 `forwardResult`。

因此：

- `retry_only` 尚有预算：Discard 后重试；
- `retry_only` 预算耗尽：Commit 当前响应；
- `retry_then_switch` 尚有预算：Discard 后重试同一 provider；
- `retry_then_switch` 预算耗尽：先选择并保留替代 provider，再 Discard 并切换；无法取得替代 provider 时 Commit 当前响应；
- `passthrough`：立即 Commit；
- provider 在同 provider 重试前被删除或手动禁用：`retry_only` Commit 当前响应；`retry_then_switch` 跳过剩余同 provider 重试并尝试保留替代 provider，无法取得时才 Commit。

### 4.2 协议分析管线

协议不能只由 `api_type` 决定。注册表按请求路由契约、响应 Content-Type 和 Content-Encoding 解析出稳定的 `response_protocol_id` 及 adapter；兼容 API 类型可以复用同一 adapter。

每个 adapter 同时完成：

- SSE/JSON framing；
- control、error、client-visible 事件分类；
- 错误字段提取；
- token usage 观察。

同一响应只有一个 reader/pump 和一次解压，不再给 token interceptor 与错误检测分别建立 TeeReader。SSE decoder 必须支持 LF/CRLF、注释、无空格 `data:`、多 data 行、跨 Read 边界和 EOF 尾帧；普通 JSON 使用有界增量 decoder。解析失败或不支持的编码按 fail-open 透传，并记录稳定原因。

### 4.3 探测窗口与资源上限

只有存在可能触发重试的候选规则时才缓冲响应。仅有 `passthrough` 规则时直接流式转发，同时旁路观察，不增加首字节延迟。

探测窗口在以下任一条件发生时释放：

1. adapter 识别到首个 client-visible 事件；
2. `DefaultProbeDuration` 到期；
3. 达到 `DefaultProbeMemoryLimit`；
4. 进程级 `ResponseProbeMemoryBudget` 无法继续授予内存。

默认值和硬上限使用具名常量：

```text
DefaultProbeDuration       = 2s
DefaultProbeMemoryLimit    = 256KiB
MaxProbeMemoryLimit        = 1MiB
ResponseProbeMemoryBudget  = 64MiB
MaxDecodedEventBytes       = 256KiB
```

请求体大小不得改变探测内存上限。`DefaultProbeMemoryLimit` 和进程预算统一计入本功能持有的原始前缀、解码/framing 缓冲、待处理通道字节及提取字段副本；固定大小 scratch buffer 单独受具名常量约束。压缩流的原始前缀和解压后事件分别计费，任何一次预算申请失败都立即 fail-open 并记录 `probe_release_reason`。旗舰捕获中的错误位于约 72KB，默认值覆盖该场景。

单一 pump 独占上游 `Read`。探测 timer、现有 regular/SSE idle timeout、客户端取消和内存预算通过 coordinator 驱动状态变化，禁止并发读取同一个 body。离开 probing 时停止探测 timer；最终退出时关闭 body、归还全部预算并等待 pump 结束。SSE 的 flush、客户端背压和现有 capture 字节观察必须由同一 coordinator 保留。

窗口释放后：

- 已提交的流继续做有界协议观察；
- 后续命中只记录结果和健康判定，不再改变客户端响应；
- 普通 JSON 仍由增量 decoder 观察，不能把“前缀解析不完整”等同于“未命中”。

## 5. 重试决策

`RetryDecisionEngine` 是纯逻辑组件，输入规则快照、当前观察、provider 状态和 `RetryLedger`，输出一个明确决策。

失败分类顺序固定为：fetch/transport 错误 → 401 credential refresh → 现有 HTTP status policy → 可提交 2xx 的内部错误分析。内部错误规则不覆盖 status failover；credential refresh 是同一 logical provider attempt 内的 sub-exchange，由 capture 的 `CredentialPhase` 记录，不单独消耗 `global_max_attempts` 或规则预算。

`RetryLedger` 分别记录：

- 总 logical provider attempt 数；
- 每 provider 的既有失败重试数；
- 每 `(provider_id, rule_id)` 已实际调度的规则重试数。

只有真正调度的 `retry_same` 才消耗规则重试预算。不同失败类型使用各自预算，但都受 `global_max_attempts` 硬上限约束。

当 `retry_then_switch` 存在可用替代 provider 时，决策器为切换保留最后一个全局 attempt：剩余总预算只够一次时直接切换，不再把机会花在当前 provider。UI 显示规则预算可能受到全局上限约束。

`switch_provider` 必须复用现有 `providerSwitchTracker`，动作名本身不决定 switch mode：没有可见 continuity 时是 `replacement`，已有或本次已建立可见 continuity 时才是 `failover`。selector 的 vendor isolation 和切换计数继续以该 mode 为准。

请求本地已经批准的 `retry_same` 不因 circuit breaker 随后打开而被截断，但仍尊重 provider 删除和手动禁用。这样健康状态可以及时反映失败，而规则预算仍按用户配置执行。

## 6. 健康语义

每个 attempt 只产生一次 `HealthVerdict`：`success`、`failure` 或 `neutral`。

| 结果 | HealthVerdict |
|---|---|
| 未命中且正常完成 | success |
| `retry_then_switch` 命中 | failure |
| `retry_only` 命中 | neutral |
| `passthrough` 命中 | neutral |

窗口是否已经释放不改变该表。任何已识别的内部错误都不得再走现有“2xx 即 `markSuccess`”路径。`failure` 每个 attempt 立即写入健康管理器；请求本地重试许可与跨请求 circuit breaker 分离。

RequestLog 的业务结果与健康结果也必须分离：被识别并透传的 HTTP 200 仍是上游语义失败，只是客户端 transport status 为 200。

## 7. 缓存、统计与可观测性

新增 consumer-side `RuleSetProvider` 接口。SQLite repository 保存一等规则表及单调递增的 `RuleSetRevision`。所有规则 CRUD、重排、provider 删除和配置导入统一经过 `RuleSetMutationCoordinator` 串行构造并编译完整候选集，在同一事务中写入规则和 revision；提交后只允许以更大的 revision 原子发布快照。启动时加载同一 revision 并编译，发现无效规则直接失败。不把规则塞进 runtime config 字符串，也不在每个请求中查询数据库。

`hit_count` 不在代理热路径同步更新 SQLite。命中只递增按 rule 分片的内存原子计数，后台定期交换增量并批量累加到 `InternalErrorRuleStats`；写入失败的增量保留重试，不阻塞请求。统计和时间戳不参与配置导入导出。

RequestAttempt 使用结构化 semantic error evidence，至少包含：

```text
rule_id, normalized_rule_snapshot, rule_set_revision,
matched_keywords, matched_fields, response_protocol_id,
action, decision,
window_state, probe_release_reason, result_visible_to_client
```

HTTP attempt 的 `outcome` 使用 `upstream_semantic_error`。`switch_reason` 只说明为什么离开 provider，使用稳定值 `internal_error_rule_exhausted`；规则 ID 不拼进 `switch_reason`，同 provider retry 和 passthrough 也不伪装成 provider switch。

结构化 trace 在窗口释放、规则命中、预算决策、Commit/Discard、健康判定处记录 `request_id`、provider、attempt、rule、rule-set revision 和决策。Request Capture 新增内部错误吸收/提交 termination reason，并正确区分 upstream 已读字节与 client 已写字节。

## 8. UI

- 唯一编辑入口：`/error-detection`；Provider 抽屉只读展示并提供带 provider/api_type 的新建入口。
- 列表显示 scope、API type、关键词、动作、重试次数、命中统计和启用状态，并支持调整 `position`。
- 编辑器使用 Scope、API type、Keywords、Any/All、Action、Retry count、Backoff、Enabled；v1 不显示 exhaustion behavior。
- `custom:*` 显示“不支持结构化错误检测”，不能保存 enabled 规则。
- Test Message 必须调用与运行时相同的 adapter、matcher 和 precedence resolver，显示提取字段、全部匹配规则及最终规则。
- 时间线从 structured evidence 渲染，不解析 `switch_reason` 字符串。
- 等待时间只展示探测窗口与 backoff 的上界估算，并注明不含连接和首字节等待；同时展示 `global_max_attempts` 造成的有效预算截断。

## 9. 实施顺序

新增领域逻辑放入 `internal/errorrule`，响应 pump、decoder 和 adapter 放入 `internal/responseanalysis`；`internal/proxy` 只保留编排与接线。每个目录不超过 20 个非测试代码文件。

### Phase 1：领域与配置

- Rule/Action/Stats 模型、约束、迁移和 CRUD；
- 显式排序、导入导出、provider 删除级联；
- RuleSetProvider、持久化 revision、变更协调器、单调发布和 resolver 单元测试。

### Phase 2：响应分析框架

- ProtocolRegistry、各协议 adapter、统一 SSE/JSON decoder；
- 单 pump 解压与 usage/error observers；
- PendingResponse 状态机、单 reader/单 writer、探测内存预算和取消清理；
- 原始字节无损 Commit、压缩流和边界测试。

### Phase 3：执行与观测

- RetryDecisionEngine、RetryLedger 和全局预算预留；
- `executeProxy` 接入 commit/retry/switch 决策、既有 status/credential precedence 和 switch tracker；
- HealthVerdict、RequestAttempt evidence、Request Capture 和异步 stats；
- HTTP/SSE 端到端测试。

### Phase 4：UI

- 列表、排序、编辑器、试匹配和预设；
- Provider 抽屉和尝试时间线；
- 可访问性测试与 React 覆盖率验证。

## 10. 必须覆盖的验收场景

- 旗舰捕获形态：错误为第 3 个 SSE 事件且位于约 72KB，窗口内吸收；
- `retry_only` 耗尽后逐字节保留状态、响应头、压缩编码和最终 body；
- `retry_then_switch` 在有限 `global_max_attempts` 下保留一次切换机会；
- 混合 transport/status/internal-error 失败不会串用预算；
- 同一错误在窗口内外产生相同 HealthVerdict；
- passthrough-only 规则不缓冲、不增加 TTFT；
- 正常输出包含关键词不命中；SSE 多行、跨 Read、CRLF、尾帧正确；
- gzip/brotli、解压膨胀、超大事件及并发内存预算不会突破硬上限；
- 规则重排、导入导出及请求中途修改规则保持确定性；
- 并发 CRUD、重排、provider 删除和重启后 revision 单调，运行快照不会回退；
- probe timer 与阻塞 Read 同时发生时无双读、乱序、重复提交或遗留预算；
- credential refresh、status failover 和内部错误使用明确优先级，切换 mode 与 continuity 语义一致；
- 客户端取消、timer 到期、解析失败和 provider 删除均无 goroutine/body 泄漏；
- Go 覆盖率不低于 90%，React 覆盖率不低于 40%。

## 11. 主要代码位置

- 重试执行：`internal/proxy/handler_execute_proxy.go`
- 响应提交：`internal/proxy/handler_forward.go`
- 失败语义：`internal/proxy/provider_failure_semantics.go`
- 传输与响应体：`internal/proxy/transport.go`
- 现有 SSE/usage 解析：`internal/proxy/interceptor.go`
- 规则领域：`internal/errorrule/`（新增）
- 响应分析：`internal/responseanalysis/`（新增）
- 尝试模型：`internal/model/model.go`
- 缓存参考：`internal/store/cached_store.go`
- UI 参考：`web/src/pages/RoutingPolicies.tsx`、`web/src/components/RequestAttemptTimeline.tsx`
