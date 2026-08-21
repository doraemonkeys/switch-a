# Token 使用量分析面板执行计划

## 目标

在管理端 `Logs` 页面增加 Token Usage 分析面板，基于现有请求日志展示指定时间窗内的 token 总览、趋势和主要消耗来源。首版不做价格/费用估算。实现 analytics 前先补齐 OpenAI `cache_write_tokens` 采集，复用现有字段且不回填历史日志；HTTP/SSE/WebSocket usage 合并与日志写入行为不得回归。

## 核心语义

- `request_logs` 中的 token 字段是 analytics 的唯一数据源；analytics 只读、不重新解析响应、不回写或修正原始字段。
- OpenAI `input_tokens_details.cache_write_tokens` 写入现有 `cache_creation_input_tokens`，不新增数据库列；可选明细保留存在性：显式 `0` 落库为 `0`，缺失落库为 `NULL`，历史 `NULL` 视为未知。
- 在共享 `apicontract` 中增加 token usage 语义分类，由独立 analytics 领域层统一投影；SQL、handler 和 UI 不硬编码 provider ID、名称或供应商分支。
- Anthropic Messages：`canonical_input = prompt + cache_read + cache_creation`，`fresh_input = prompt`，`canonical_total = canonical_input + completion`。
- OpenAI-compatible：`canonical_input = prompt`；cache read/write 均已观测且关系有效时，`fresh_input = prompt - cache_read - cache_creation`，否则未能归因的输入进入 `unclassified_input`；`canonical_output = completion`，reasoning 已包含在 output，优先使用已落库 total。
- Google Generate Content：`canonical_input = prompt`；cache read 已观测时计算 `fresh_input = prompt - cache_read`，否则未能归因的输入进入 `unclassified_input`；正 total 存在时以 `canonical_output = total - prompt`、`reasoning = total - prompt - completion` 投影，残差为负则不可比较；total 为 0 或缺失时仅在 input/output 完整时派生。
- 未知或 custom 协议只使用已落库 input/output/total，不派生 fresh/cache 比例，并计入不可比较请求数。
- 对已落库字段校验负数、子项大于父项及协议总量关系；不静默修正，异常请求只进入数据质量计数。reasoning 属于 output、cache 属于 input，不得重复累加。
- 主卡、趋势和排行只聚合 canonical input/output/total 完整且一致的请求，保证 `Total = Input + Output`。无法可靠细分的输入和输出分别进入 `unclassified_input`、`unclassified_output`，不得冒充 fresh 或 standard。
- `observed_requests` 指至少存在一个核心 token 字段的请求；`comparable_requests` 指 canonical 三项完整且通过校验的请求；`without_usage_requests = total_requests - observed_requests`。
- `coverage.rate = comparable_requests / total_requests`，只表示当前流量被 Token 指标覆盖的范围，不代表解析器健康；非生成请求和失败请求可能正常落入 `without_usage_requests`，不得显示为 “missing telemetry” 告警。
- `data_quality.quality_rate = comparable_requests / observed_requests`；只有已观测但部分、异常或未知语义的请求触发数据质量提示。平均 token 的分母固定为 `comparable_requests`。
- 满足过滤条件且位于 `[start, end)` 的所有日志进入 `total_requests`；token 聚合不依赖 `service_outcome` 版本。

## 界面美学设计与布局规范

> [!IMPORTANT]
> **作者特别强调（请勿删除此条）**：开发时**严禁参考项目中其他历史统计界面的简陋 UI（如旧版纯 div 竖条、字母缩写图标等）**。本面板必须完全独立按照本文档制定的现代化 Observability 视觉规范、精细化线框图、微型堆叠条（Micro-Stacked Bars）与交互浮层全新实现。

为了确保 Token 分析面板既具备顶级可观测平台（如 Datadog / Langfuse / Vercel）的高信息密度与工程专业感，又保持现代 SaaS 的极致精致美感与直观交互，面板遵循以下设计规范：

### 1. 配色与语义映射系统 (Color & Semantic Mapping)

| 语义维度 | 主题色调 | Tailwind 色值 | HEX 色值 | 视觉含义与规范 |
| :--- | :--- | :--- | :--- | :--- |
| **Total Tokens** | 沉稳靛蓝 | `text-indigo-600 dark:text-indigo-400` / `bg-indigo-500` | `#6366f1` | 全局核心体量焦点，沉稳醒目 |
| **Fresh Input** | 蔚蓝 | `text-sky-600 dark:text-sky-400` / `bg-sky-500` | `#0284c7` | 常规非缓存 Prompt 输入体量 |
| **Cache Read** | 翡翠绿/青绿 | `text-emerald-600 dark:text-emerald-400` / `bg-emerald-500` | `#10b981` | 命中缓存输入（高性价比/降本加速，醒目绿） |
| **Cache Creation** | 暖琥珀 | `text-amber-600 dark:text-amber-400` / `bg-amber-500` | `#d97706` | 写入缓存输入（首字开销，暖色） |
| **Output (Standard)** | 幽紫 | `text-violet-600 dark:text-violet-400` / `bg-violet-500` | `#8b5cf6` | 模型常规生成体量 |
| **Reasoning Tokens** | 洋红/洋紫 | `text-fuchsia-600 dark:text-fuchsia-400` / `bg-fuchsia-500` | `#d946ef` | 思考链（CoT/Thought，嵌套于 Output 内部） |
| **Unclassified Input** | 中性灰蓝 | `text-slate-500` / `bg-slate-400` | `#94a3b8` | 已计入 Input、但缓存明细缺失而无法可靠归因的体量 |
| **Unclassified Output** | 中性灰 | `text-zinc-500` / `bg-zinc-400` | `#a1a1aa` | 已计入 Output、但无法可靠细分 reasoning 的体量 |
| **Coverage & Quality** | 灰板岩/薄荷 | `text-slate-600 dark:text-slate-400` / `bg-slate-200` | `#64748b` | 指标覆盖范围与已观测数据质量 |

### 2. 视觉层级、排版与微交互

- **卡片式架构 (Container & Modern Elevation)**：
  - 容器采用 `bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs hover:shadow-md transition-all`。
  - 卡片顶部标头配以语义图标徽章（使用 `lucide-react`，如 `Layers`, `ArrowDownLeft`, `ArrowUpRight`, `Activity`），搭配半透明底色圆角矩形（`p-2 rounded-xl bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600`）。
- **微型嵌套堆叠条 (In-card Micro Stacked Bars)**：
  - 在 **Total Tokens** 下方展示 Input vs Output 的比例横条；
  - 在 **Input Tokens** 下方展示 `[ 🟢 Cache Read | 🟠 Cache Creation | ⚪ Fresh Input | ◻ Unclassified Input ]` 堆叠微条，鼠标悬停各段即显示具体占比与数量；
  - 在 **Output Tokens** 下方展示 `[ 🟣 Reasoning CoT | 🟢 Standard Output | ⚪ Unclassified Output ]`，零值段自动省略；
  - 彻底消除心算负担，通过视觉比例直观感知模型工作负载类型（如推理密集 vs 缓存密集）。
- **等宽数字与分级展示 (Tabular Typography)**：
  - 核心数值采用 `font-mono tracking-tight font-bold text-2xl`，在大字号下方搭配紧凑格式（`1.42M`、`380.5K`），辅助文本标注千分位完整数值（`1,420,530 tokens`）。
- **Top 排行榜复合多段进度条 (Multi-Segment Progress Matrix)**：
  - 排行榜每行按 `[Fresh | Cache Read | Cache Creation | Unclassified Input | Standard Output | Reasoning | Unclassified Output]` 分段，零值段省略；所有分段之和必须等于该行 canonical total。
  - 一眼看出如 DeepSeek 主要是 Reasoning 占大头，Claude 主要是 Cache Read 占大头。
- **时序交互与悬浮导轨 (Interactive Chart & Crosshair Tooltip)**：
  - 具备刻度网格线（`border-dashed border-slate-200 dark:border-slate-800`）、顶部图例交互（可点击高亮特定序列）、平滑悬浮游标以及高精度的半透明毛玻璃悬浮卡片。

---

## UI 效果与布局预览

### 1. 全景面板线框图 (Full Panel Wireframe)

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│ 📊 Token Usage Analytics                         [ ⚡ 24h (Live) ▼ ] [ 1h bucket ▼ ] [ 🔄 Refresh ] [ ℹ️ ] │
│ Global aggregated volume & efficiency  •  Coverage: 98.2%  •  Observed-data quality: 100%                   │
├─────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                                             │
│ ┌─ ⚡ TOTAL TOKENS ────────────┐ ┌─ 📥 INPUT TOKENS ─────────────┐ ┌─ 📤 OUTPUT TOKENS ────────────┐ ┌─ 🎯 EFFICIENCY & QUALITY ──┐│
│ │  12.45 M                     │ │  8.20 M                65.9%  │ │  4.25 M                34.1% │ │  38.0% Cache Hit Rate     ││
│ │  12,451,200 total tokens     │ │  8,204,110 tokens             │ │  4,247,090 tokens            │ │  19.3% Reasoning Ratio     ││
│ │                              │ │                               │ │                              │ │                            ││
│ │  [ 8.2M Input | 4.25M Output ]│ │  [ 38% Hit | 5% W | 57% Fresh]│ │  [ 19% CoT | 81% Normal ]    │ │  Avg: 8.7K tok / obs. req  ││
│ │  ─────────────────────────── │ │  ──────────────────────────── │ │  ─────────────────────────── │ │  ───────────────────────── ││
│ │  👥 1,430 Observed Reqs      │ │  🟢 Cache Read: 3.12M (38.0%) │ │  🟣 Reasoning: 820.4K (19.3%)│ │  Coverage: 1430/1456       ││
│ │  📈 Peak: 1.2M tok / hour    │ │  🟠 Cache Creat: 420K  (5.1%) │ │  🟢 Std Output: 3.43M (80.7%)│ │  Quality: 1430/1430        ││
│ └──────────────────────────────┘ └───────────────────────────────┘ └──────────────────────────────┘ └────────────────────────────┘│
│                                                                                                             │
├─────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│  📈 Token Consumption Trend Over Time                                                                       │
│  [ Filter: ■ Fresh  ■ Cache Read  ■ Cache Create  ■ Unknown In  ■ Standard  ■ Reasoning  ■ Unknown Out ]     │
│                                                                                                             │
│   Volume                                                                                                    │
│    1.2M ┤- - - - - - - - - - - - - - - - - - - - - - - - - - - - - ┌┬┐ - - - - - - - - - - - - - - - - -   │
│    900K ┤- - - - - - - - - - - - - - - - - - - - - - ┌┬┐ - - - - - ├┼┤ - - - - - - - - - - - - - - - - -   │
│    600K ┤- - - - - - - - - - - - - - - ┌┬┐ - - ┌┬┐ - ├┼┤ - - ┌┬┐ - ├┼┤ - - - - - - - - - - - - - - - - -   │
│    300K ┤- - - - - - - - ┌┬┐ - - ┌┬┐ - ├┼┤ - - ├┼┤ - ├┼┤ - - ├┼┤ - ├┼┤ - - ┌┬┐ - - - - - - - - - - - - -   │
│      0K ┴───┬───┬───┬───┴┴┴───┬──┴┴┴───┴┴┴───┬─┴┴┴───┴┴┴───┬─┴┴┴───┴┴┴───┬──┴┴┴───┬───┬───┬───┬───────────│
│           00:00   02:00   04:00   06:00   08:00   10:00   12:00   14:00   16:00   18:00   20:00   22:00     │
│                                                                                                             │
├─────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│  Top Breakdown Matrix                                                                                       │
│                                                                                                             │
│  ┌─ 🏢 Top Providers ──────────────────────────────┐ ┌─ 🤖 Top Models ────────────────────────────────────┐│
│  │ 1. Anthropic (Direct)                           │ │ 1. claude-3-7-sonnet-20250219                      ││
│  │    7.42 M (59.6%)   840 reqs                    │ │    6.12 M (49.2%)   620 reqs                       ││
│  │    [ ▇▇▇ CacheRead | ▇ Fresh | ▇ Unknown Out ]  │ │    [ ▇▇▇ CacheRead | ▇ Fresh | ▇ Unknown Out ]   ││
│  │                                                 │ │                                                    ││
│  │ 2. OpenAI Official                              │ │ 2. gpt-4o (2024-11-20)                             ││
│  │    3.81 M (30.6%)   510 reqs                    │ │    3.10 M (24.9%)   410 reqs                       ││
│  │    [ ▇ CacheRead | ▇▇ Fresh | ▇ Standard | ▇ CoT ]│ │   [ ▇ CacheRead | ▇ Fresh | ▇ Standard | ▇ CoT ] ││
│  │                                                 │ │                                                    ││
│  │ 3. DeepSeek Provider                            │ │ 3. deepseek-reasoner                               ││
│  │    1.22 M (9.8%)    80 reqs                     │ │    1.22 M (9.8%)    80 reqs                        ││
│  │    [ ▇ Fresh | ▇ Standard | ▇▇▇ Reasoning CoT ] │ │    [ ▇ Fresh | ▇ Standard | ▇▇▇ Reasoning CoT ]    ││
│  └─────────────────────────────────────────────────┘ └────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

### 2. 核心指标卡片精细化结构 (Card Deep Dive)

```text
┌─ 📥 INPUT TOKENS ──────────────────────────────────────────────────────────┐
│  <Icon: ArrowDownLeft class="text-sky-500 bg-sky-50 p-2 rounded-xl" />     │
│  8,204,110 tokens                     <Badge variant="sky">65.9% Total</Badge>│
│                                                                            │
│  ┌─ Micro Breakdown Bar ─────────────────────────────────────────────────┐ │
│  │  [  🟢 38.0% Cache Read  ][ 🟠 5.1% Creat ][      ⚪ 56.9% Fresh      ] │ │
│  └───────────────────────────────────────────────────────────────────────┘ │
│                                                                            │
│  • 🟢 Cache Read (Hit):     3,120,400  (38.0% of input)                    │
│  • 🟠 Cache Creation:         420,000  ( 5.1% of input)                    │
│  • ⚪ Uncached Fresh:       4,663,710  (56.9% of input)                    │
└────────────────────────────────────────────────────────────────────────────┘

┌─ 📤 OUTPUT TOKENS ─────────────────────────────────────────────────────────┐
│  <Icon: ArrowUpRight class="text-violet-500 bg-violet-50 p-2 rounded-xl" />│
│  4,247,090 tokens                  <Badge variant="violet">34.1% Total</Badge>│
│                                                                            │
│  ┌─ Micro Breakdown Bar ─────────────────────────────────────────────────┐ │
│  │  [       🟣 19.3% Reasoning CoT       ][      🟢 80.7% Standard      ] │ │
│  └───────────────────────────────────────────────────────────────────────┘ │
│                                                                            │
│  • 🟣 Reasoning (CoT):        820,400  (19.3% of output)                   │
│  • 🟢 Standard Output:      3,426,690  (80.7% of output)                   │
└────────────────────────────────────────────────────────────────────────────┘
```

---

### 3. 图表悬停浮层卡片设计 (Chart Hover Tooltip)

当鼠标悬停于柱状图某时间桶（Bucket）时，展示半透明毛玻璃浮层明细卡片：

```text
┌────────────────────────────────────────────────────────────┐
│ ⏱️ 14:00 - 15:00 (1h bucket)        112 observed / 114 total │
├────────────────────────────────────────────────────────────┤
│ Total Tokens                              842,100 tokens   │
├────────────────────────────────────────────────────────────┤
│ 📥 Input                                  540,000  (64.1%) │
│   ├─ 🟢 Cache Read (Hit)                  180,000  (33.3%) │
│   ├─ 🟠 Cache Creation                     45,000  ( 8.3%) │
│   └─ ⚪ Fresh Prompt                      315,000  (58.4%) │
│ 📤 Output                                 302,100  (35.9%) │
│   ├─ 🟣 Reasoning (CoT)                    95,000  (31.4%) │
│   └─ 🟢 Standard Output                   207,100  (68.6%) │
└────────────────────────────────────────────────────────────┘
```

---

### 4. 边界态与空态视觉呈现 (Edge & Empty States)

- **Coverage 与数据质量提示**：
  Coverage 始终使用中性色，仅说明指标覆盖范围；低 Coverage 不告警。只有 `data_quality.quality_rate < 100%` 时显示琥珀色提示，并列出部分、异常和未知语义计数。
- **全空状态 (Zero / No Data State)**：
  图表与卡片区域呈现带柔和浅色背景与虚线边框的 Empty State 占位符，配以 Lucide `BarChart2` 占位图标及友好提示 `No token telemetry recorded in this time window`。
- **加载状态 (Skeleton Shimmer)**：
  卡片与图表保留固定比例骨架，使用 Tailwind `animate-pulse bg-slate-100 dark:bg-slate-800 rounded-xl` 块，加载过渡流畅无布局抖动（Layout Shift）。

---

## 执行步骤

1. **补齐 Cache Write 采集语义**
   - HTTP/SSE adapter 与 WebSocket token parser 均解析 OpenAI `cache_write_tokens`，统一映射至现有 `CacheCreation.InputTokens` / `cache_creation_input_tokens`；泛化原有 Claude 专属命名与注释，不新增数据库列。
   - cache read/write/reasoning 明细使用“值 + 是否出现”语义；合并时累加值并合并 presence，显式零与缺失分别写为 `0` 和 `NULL`。

2. **建立独立分析契约**
   - 新增 `TokenUsageQuery`、主指标、细分指标、Coverage、时间桶及 provider/model 排行模型。Coverage 固定包含 `total_requests`、`observed_requests`、`comparable_requests`、`without_usage_requests`、`rate`。
   - 提供 `GET /admin/api/token-usage`，支持 `period`、`granularity`、`as_of`、`provider_id`、`model`、`api_type`；现有 `/stats` 同步支持 `as_of`，仅用于对齐同一时间终点，不宣称两个 HTTP 请求共享数据库快照。
   - 响应固定包含 `summary`、`timeseries`、`by_provider`、`by_model`、`time_range`、`coverage`、`data_quality`；`data_quality` 包含 `quality_rate`、`partial_requests`、`invalid_requests`、`unknown_semantics_requests`。空集合返回 `[]`。
   - Token 数量以十进制字符串传输，request 计数和比例使用 number，避免 JavaScript 安全整数溢出。

3. **实现统一口径投影**
   - 新建独立 token analytics 模块，在 `apicontract` 中声明 Anthropic/OpenAI/Google token 语义；不得硬编码 provider ID 或名称。
   - 存储层通过统一 SQL 投影/CTE 生成 canonical input/output/total，再供所有汇总查询复用；analytics 不重新解析响应。
   - 未知协议、部分观测和异常关系进入 `data_quality`；无法可靠细分的 input/output 分别进入 `unclassified_input`、`unclassified_output`。所有摘要、桶和排行必须满足分段之和等于 canonical total。

4. **实现一致的数据库聚合快照**
   - analytics 使用独立只读 SQLite 连接执行固定数量的聚合查询，不改变现有单连接写入池；同一 token 响应内的查询运行于一个只读快照，使用 SQL 聚合且不加载原始日志。
   - 时间桶按 UTC 对齐并覆盖完整 `[start, end)`，包含首尾部分桶、补齐空桶并限制最大桶数；排行使用命名常量 Top-N 和稳定次序。
   - 批量映射 provider 名称，已删除 provider 回退显示 ID。用 `EXPLAIN QUERY PLAN` 和并发 `InsertLog` 验证索引与读写隔离，analytics 不得导致现有 2 秒日志写入超时。

5. **接入管理 API 与可观测性**
   - 复用可注入时钟的 analytics window 校验器；handler 只解析参数、调用分析接口和映射 DTO，不重算指标。
   - 查询开始、完成和失败记录结构化日志，包含 operation ID、时间窗、粒度、过滤条件、桶数、不可比较请求数及耗时，不记录凭据或请求正文。
   - 前端增加严格运行时解码，非法或缺字段响应进入可诊断错误态。

6. **构建前端面板**
   - 将 period/granularity/as-of 提升为共享 analytics window state；刷新时同时更新 outcome/token 的时间边界。
   - 面板明确标注 Global，不静默继承 Logs 表格过滤器；存在表格过滤时提示其不影响全局分析。
   - 构建 4 组现代 Hero 卡片（Total Tokens、Input Tokens、Output Tokens、Efficiency & Quality），内部集成守恒的微型堆叠条；采用 `lucide-react` 矢量图标与 `font-mono` 等宽排版。
   - 构建交互式时序趋势图（支持刻度参考网格、图例交互、平滑悬浮游标与毛玻璃 Rich Tooltip 浮层）以及 Top 排行榜复合多段进度条（Multi-segment Bars）。
   - 完善数据质量状态条（Data Quality Pill）、骨架屏加载动画（Skeleton Shimmer）与空态占位，覆盖响应式与键盘可访问性。

7. **测试与验收**
   - 从 `docs/.capture` 提炼不含正文和凭据的 usage fixture，覆盖 OpenAI cache write 的正数/显式零/缺失、Claude 累计 usage、缺失 Content-Type、HTTP/SSE/WebSocket 与 usage 合并。
   - Go 覆盖 Google thoughts 残差、历史 NULL、异常关系、input/output 分段守恒、Coverage/Quality、时间边界、过滤、Top-N、空桶、存储失败、只读快照及 analytics 并发下日志写入，覆盖率不低于 90%。
   - React 覆盖十进制 token 解码、共享时间边界、数值格式化、Coverage 中性展示、Quality 告警、Unclassified Input/Output、空态/错误态、趋势和排行。
   - 运行 `make ci` 与 `go test ./internal/responseanalysis/...`，完成桌面和窄屏人工验收。
