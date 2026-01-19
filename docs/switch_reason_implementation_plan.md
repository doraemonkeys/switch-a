# 实现计划：Provider 切换原因透明化 ✅ COMPLETED

## 背景

用户设置 `max_retries=2`，期望看到 3 次尝试，但实际只看到 2 次，却不知道为什么。
当前 UI 缺失的关键信息：
1. Attempt Timeline 没有显示 Provider 切换原因（熔断 vs 重试耗尽 vs 永久错误）
2. Monitor 页面没有显示熔断器详情（原因、恢复时间）

---

## 实现优先级

| 优先级 | 改进项 | 收益 |
|--------|--------|------|
| **P0** | 在 Attempt Timeline 显示切换原因 | 直接回答"为什么只重试了1次" |
| **P1** | Monitor 页面显示熔断详情（原因+恢复时间） | 用户可以查看系统状态 |
| **P2** | 日志页面的 tooltip 解释重试机制 | 减少用户困惑 |

---

## P0：在 Attempt Timeline 显示切换原因

### 阶段 1：后端模型变更

**文件：`internal/model/model.go`**

```go
// RequestAttempt represents a single attempt within a request (for retry tracking).
type RequestAttempt struct {
    ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
    RequestID    string    `gorm:"index" json:"request_id"`
    ProviderID   string    `json:"provider_id"`
    Attempt      int       `json:"attempt"`
    StatusCode   int       `json:"status_code"`
    Error        string    `json:"error"`
    BodySnippet  string    `json:"body_snippet,omitempty"`
    LatencyMs    int64     `json:"latency_ms"`
    SwitchReason string    `json:"switch_reason,omitempty"` // 新增字段
    CreatedAt    time.Time `json:"created_at"`
}
```

**SwitchReason 可能的值：**
- `""` - 无切换（成功或继续在同一 Provider 重试）
- `"max_retries_exhausted"` - 该 Provider 重试次数用完
- `"circuit_breaker_triggered"` - 熔断器触发
- `"permanent_error_401"` - 认证错误（不可重试）
- `"permanent_error_402"` - 付款错误（不可重试）
- `"permanent_error_403"` - 权限错误（不可重试）

### 阶段 2：数据库迁移

**文件：`internal/store/sqlite.go`**

在 `AutoMigrate` 中添加 `SwitchReason` 字段迁移（GORM 自动处理）。

### 阶段 3：后端逻辑修改

**文件：`internal/proxy/constants.go`（新建）**

```go
package proxy

// SwitchReason 常量定义 Provider 切换的原因
const (
    SwitchReasonMaxRetriesExhausted     = "max_retries_exhausted"
    SwitchReasonCircuitBreakerTriggered = "circuit_breaker_triggered"
)
```

**文件：`internal/proxy/handler.go`**

1. **修改 `tryIncrementAndExhaustsProvider` 方法签名：**

```go
// tryIncrementAndExhaustsProvider attempts to increment the provider retry counter.
// Returns (exhausted bool, switchReason string).
// exhausted=true means we should switch to a different provider.
// switchReason is non-empty only when exhausted=true.
//
// 注意：由于 markFailure 在 forwardToProvider 中调用，而 IsAvailable 检查
// 在 tryIncrementAndExhaustsProvider 中进行（发生在 markFailure 之后），
// 如果本次失败恰好触发了熔断器，switch_reason 会正确记录为
// "circuit_breaker_triggered"。这意味着 switch_reason 可以反映
// "本次失败导致熔断"的情况。
func (h *Handler) tryIncrementAndExhaustsProvider(ctx context.Context, state *retryState) (bool, string) {
    maxRetries := max(0, state.currentProvider.MaxRetries)
    
    // 判断是否需要强制切换（永久错误或熔断）
    if shouldForceProviderSwitch(state.statusCode) {
        return true, fmt.Sprintf("permanent_error_%d", state.statusCode)
    }
    if h.health != nil && !h.health.IsAvailable(ctx, state.currentProvider.ID) {
        return true, SwitchReasonCircuitBreakerTriggered
    }
    
    // 正常重试逻辑：检查是否还有重试次数
    if state.providerAttempt < maxRetries {
        state.providerAttempt++
        return false, ""
    }
    return true, SwitchReasonMaxRetriesExhausted
}
```

2. **修改 `recordAttempt` 以接收 switchReason 参数：**

```go
func (h *Handler) recordAttempt(pctx *proxyContext, state *retryState, result forwardResult, attempt int, attemptStart time.Time, switchReason string) {
    attemptRecord := model.RequestAttempt{
        RequestID:    pctx.requestID,
        ProviderID:   state.currentProvider.ID,
        Attempt:      attempt,
        StatusCode:   result.statusCode,
        BodySnippet:  result.bodySnippet,
        LatencyMs:    time.Since(attemptStart).Milliseconds(),
        SwitchReason: switchReason, // 直接传入
        CreatedAt:    time.Now(),
    }
    if result.err != nil {
        attemptRecord.Error = result.err.Error()
    }
    pctx.attempts = append(pctx.attempts, attemptRecord)
}
```

3. **修改 `executeProxy` 调用顺序（关键修复）：**

原代码执行顺序有问题：`recordAttempt` 在 `tryIncrementAndExhaustsProvider` 之前调用，
导致 `SwitchReason` 永远是空字符串。需要调整为：

```go
// 在 executeProxy 循环中
// 原代码（错误顺序）:
//   h.recordAttempt(pctx, state, result, attempt, attemptStart)
//   if result.done { break }
//   if h.tryIncrementAndExhaustsProvider(ctx, state) { ... }

// 修复后（正确顺序）:
if result.done {
    // 成功或最终失败，无需切换
    h.recordAttempt(pctx, state, result, attempt, attemptStart, "")
    break
}

// 先判断是否需要切换 Provider
exhausted, switchReason := h.tryIncrementAndExhaustsProvider(ctx, state)

// 再记录 attempt（此时 switchReason 已确定）
h.recordAttempt(pctx, state, result, attempt, attemptStart, switchReason)

if exhausted {
    h.excludeCurrentProvider(state)
}
```

### 阶段 4：前端类型更新

**文件：`web/src/api/types.ts`**

```typescript
/** Represents a single attempt within a request (for retry tracking) */
export interface RequestAttempt {
  id: number;
  request_id: string;
  provider_id: string;
  attempt: number;
  status_code: number;
  error: string;
  body_snippet?: string;
  latency_ms: number;
  switch_reason?: string; // 新增
  created_at: string;
}
```

### 阶段 5：UI 组件更新

**文件：`web/src/components/RequestAttemptTimeline.tsx`**

```tsx
// 添加 switch reason 标签映射函数
function getSwitchReasonLabel(reason: string): { text: string; icon: string } | null {
  switch (reason) {
    case "circuit_breaker_triggered":
      return { text: "Circuit breaker triggered — switched provider", icon: "⚡" };
    case "max_retries_exhausted":
      return { text: "Max retries reached — switched provider", icon: "🔄" };
    case "permanent_error_401":
      return { text: "Auth error (401) — switched provider", icon: "🔐" };
    case "permanent_error_402":
      return { text: "Payment required (402) — switched provider", icon: "💳" };
    case "permanent_error_403":
      return { text: "Forbidden (403) — switched provider", icon: "🚫" };
    default:
      return null;
  }
}

// 在 AttemptNode 组件中添加显示逻辑
function AttemptNode({ attempt, isLast, providerName }: AttemptNodeProps) {
  // ... existing code ...
  
  const switchReasonInfo = attempt.switch_reason 
    ? getSwitchReasonLabel(attempt.switch_reason) 
    : null;
  
  return (
    <div className="relative pl-8">
      {/* ... existing timeline dot and card ... */}
      
      <div className={`p-3 rounded-lg border ${getCardClasses()}`}>
        {/* ... existing header, provider, error display ... */}
        
        {/* Switch reason indicator - 新增 */}
        {switchReasonInfo && (
          <div className="mt-2 flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 px-2 py-1 rounded">
            <span>{switchReasonInfo.icon}</span>
            <span>{switchReasonInfo.text}</span>
          </div>
        )}
      </div>
    </div>
  );
}
```

### 阶段 6：测试

1. **后端单元测试** (`internal/proxy/handler_test.go`)：
   - 测试 `tryIncrementAndExhaustsProvider` 返回正确的 switchReason
   - 测试各种切换场景

2. **前端单元测试** (`web/src/components/RequestAttemptTimeline.test.tsx`)：
   - 测试 switch_reason 显示正确的标签
   - 测试无 switch_reason 时不显示

---

## P1：Monitor 页面显示熔断详情

### 阶段 1：确认后端数据已存在

**现状分析：**
- `model.HealthState` 已有 `DisabledUntil` 和 `DisabledReason` 字段
- `HealthState` 在 `/admin/status` API 中已返回
- 前端 `web/src/api/types.ts` 的 `HealthState` 类型已包含这些字段

✅ 后端无需修改，数据已可用。

### 阶段 2：UI 组件更新

**文件：`web/src/pages/Monitor.tsx`**

1. **添加时间格式化工具函数：**

```tsx
// 计算恢复剩余时间
function formatTimeUntil(isoDate: string): string {
  const target = new Date(isoDate).getTime();
  const now = Date.now();
  const diffMs = target - now;
  
  if (diffMs <= 0) return "recovering...";
  
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

// 格式化禁用原因（移除 "auto: " 前缀使其更易读）
function formatDisabledReason(reason: string): string {
  if (reason.startsWith("auto: ")) {
    return reason.slice(6); // 移除 "auto: " 前缀
  }
  if (reason.startsWith("manual: ")) {
    return `Manual: ${reason.slice(8)}`;
  }
  return reason;
}
```

2. **增强 Provider Status 卡片：**

```tsx
{status.providers.map((provider) => (
  <div
    key={provider.id}
    className="flex items-center justify-between p-3 rounded-lg bg-bg-tertiary hover:bg-bg-hover transition-colors"
  >
    {/* 注意：添加 flex-1 使内容区域正确填充剩余空间 */}
    <div className="flex items-center gap-3 min-w-0 flex-1">
      <StatusDot
        enabled={provider.enabled}
        available={provider.health?.available ?? true}
        disabledReason={provider.health?.disabled_reason}
      />
      <div className="flex flex-col min-w-0">
        <span className="truncate text-sm font-medium text-text-primary">
          {provider.name}
        </span>
        {/* 新增：显示熔断原因和恢复时间 */}
        {!provider.health?.available && provider.health?.disabled_reason && (
          <span className="text-xs text-amber-600 dark:text-amber-400 truncate">
            {formatDisabledReason(provider.health.disabled_reason)}
            {provider.health.disabled_until && (
              <> · Recovers in {formatTimeUntil(provider.health.disabled_until)}</>
            )}
          </span>
        )}
      </div>
    </div>
    {provider.current_requests > 0 && (
      <span className="badge badge-primary text-xs flex-shrink-0">
        {provider.current_requests} req
      </span>
    )}
  </div>
))}
```

3. **增强 StatusDot 组件 tooltip：**

```tsx
interface StatusDotProps {
  enabled: boolean;
  available: boolean;
  disabledReason?: string | null; // 新增参数
}

function StatusDot({ enabled, available, disabledReason }: StatusDotProps) {
  if (!enabled) {
    return (
      <span
        className="w-2.5 h-2.5 rounded-full bg-gray-400 flex-shrink-0"
        title="Disabled by configuration"
        role="img"
        aria-label="Disabled"
      />
    );
  }
  if (!available) {
    const title = disabledReason 
      ? `Unhealthy: ${disabledReason.replace(/^(auto|manual): /, "")}`
      : "Unhealthy";
    return (
      <span
        className="w-2.5 h-2.5 rounded-full bg-red-500 flex-shrink-0"
        title={title}
        role="img"
        aria-label={title}
      />
    );
  }
  return (
    <span
      className="w-2.5 h-2.5 rounded-full bg-green-500 flex-shrink-0"
      title="Healthy"
      role="img"
      aria-label="Healthy"
    />
  );
}
```

4. **更新 StatusDot 调用处（传递 disabledReason 参数）：**

```tsx
<StatusDot
  enabled={provider.enabled}
  available={provider.health?.available ?? true}
  disabledReason={provider.health?.disabled_reason}  // 新增这行
/>
```

### 阶段 3：测试

**文件：`web/src/pages/Monitor.test.tsx`**

- 测试 disabled_reason 正确显示
- 测试 disabled_until 倒计时格式
- 测试 StatusDot tooltip 内容
- 测试 `formatTimeUntil` 边界情况：
  - 负数（已过期时间）→ 返回 "recovering..."
  - 0（刚好到期）→ 返回 "recovering..."
  - 跨秒/分钟/小时边界（如 59s→1m, 59m→1h）

---

## P2：日志页面 Tooltip 解释重试机制

### 实现方案

**文件：`web/src/pages/Logs.tsx`**

> ⚠️ 注意：使用 `title` 属性的 tooltip 在移动端不可用，且样式不可控。
> 本项目不使用 Radix UI，因此采用自定义 CSS Tooltip 实现。

**推荐方案：自定义 CSS Tooltip**

```tsx
// 添加 InfoTooltip 组件
function InfoTooltip({ text }: { text: string }) {
  return (
    <span className="relative group cursor-help text-text-muted">
      ℹ️
      <span className="invisible group-hover:visible absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-64 p-2 text-xs text-white bg-gray-900 rounded-lg shadow-lg z-50 whitespace-normal">
        {text}
        <span className="absolute left-1/2 -translate-x-1/2 top-full border-4 border-transparent border-t-gray-900" />
      </span>
    </span>
  );
}

// 使用
<th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
  <span className="inline-flex items-center gap-1">
    Retries
    <InfoTooltip text="Retry count shows the number of additional attempts after the initial request. A request with max_retries=2 can have up to 3 attempts total (1 initial + 2 retries). Circuit breaker or permanent errors (401/402/403) may interrupt retries early." />
  </span>
</th>
```

---

## 实施顺序

```
Week 1: P0 阶段 1-3（后端变更）
  ├─ 模型变更 + 迁移
  ├─ constants.go 新建（SwitchReason 常量）
  ├─ handler.go 逻辑修改
  └─ 后端单元测试

Week 2: P0 阶段 4-6（前端变更）
  ├─ types.ts 更新
  ├─ RequestAttemptTimeline.tsx 更新
  └─ 前端单元测试

Week 3: P1（Monitor 页面增强）
  ├─ Monitor.tsx 更新
  ├─ StatusDot 增强
  └─ 测试

Week 4: P2 + 集成测试
  ├─ Logs.tsx tooltip 添加
  └─ E2E 测试验证完整流程
```

---

## 验收标准

### P0 验收
- [ ] 点击日志详情，在 Attempt Timeline 中可以看到切换原因标签
- [ ] 熔断触发时显示 "⚡ Circuit breaker triggered — switched provider"
- [ ] 重试耗尽时显示 "🔄 Max retries reached — switched provider"
- [ ] 永久错误时显示对应的错误类型

### P1 验收
- [ ] Monitor 页面的 Provider Status 列表中，不可用的 Provider 显示原因
- [ ] 显示恢复倒计时（如 "Recovers in 2m 30s"）
- [ ] 鼠标悬停在状态点上显示详细 tooltip

### P2 验收
- [ ] Logs 页面 Retries 列标题有帮助信息图标
- [ ] 悬停显示重试机制说明

---

## 风险与缓解

| 风险 | 缓解措施 |
|------|----------|
| 数据库迁移影响现有数据 | SwitchReason 字段使用 `omitempty`，旧数据显示为空 |
| 性能影响 | SwitchReason 是小字符串，对性能无显著影响 |
| 前端兼容性 | TypeScript 类型使用 `?` 可选标记，向后兼容 |
| 熔断器及时性判断 | 由于 `markFailure` 在 `forwardToProvider` 中同步调用，且 `IsAvailable` 检查在其之后执行，如果本次失败恰好触发熔断，`switch_reason` 会正确记录为 `"circuit_breaker_triggered"`。无需特殊处理。 |