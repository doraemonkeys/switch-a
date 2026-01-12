# API 补充实现计划

---

## 📋 阶段总览

| 阶段 | 功能 | 优先级 | 预估复杂度 | 状态 |
|------|------|--------|------------|------|
| 1 | Provider 批量操作 API | 🔴高 | ⭐⭐ | ✅ 已完成 |
| 2 | 日志查询增强（过滤+排序） | 🔴高 | ⭐⭐⭐ | ✅ 已完成 |
| 3 | 系统统计摘要 API（基础版） | 🔴高 | ⭐⭐⭐ | ⬜ 待开始 |
| 4 | 统计 API 增强（时间序列） | 🟡中 | ⭐⭐⭐ | ⬜ 待开始 |
| 5 | Group 快捷操作 API | 🟡中 | ⭐ | ⬜ 待开始 |
| 6 | 配置导出/导入 API | 🟡中 | ⭐⭐⭐ | ⬜ 待开始 |

---

## 阶段 1：Provider 批量操作 API

### 目标
实现统一的批量操作端点，支持对多个 Provider 执行 reset/enable/disable/delete 操作。

### API 定义

```
POST /admin/api/providers/batch
```

**请求体**：
```json
{
  "action": "reset",  // "reset" | "enable" | "disable" | "delete"
  "ids": ["provider-1", "provider-2", "provider-3"]
}
```

**成功响应** (200)：
```json
{
  "success": true,
  "affected": 3,
  "results": [
    {"id": "provider-1", "success": true},
    {"id": "provider-2", "success": true},
    {"id": "provider-3", "success": true}
  ]
}
```

**部分失败响应** (207)：
```json
{
  "success": false,
  "affected": 2,
  "results": [
    {"id": "provider-1", "success": true},
    {"id": "provider-2", "success": true},
    {"id": "provider-3", "success": false, "error": "provider not found"}
  ]
}
```

### 实现要点
1. 在 `internal/admin/handlers.go` 添加 `BatchProviderAction` handler
2. 验证 action 参数合法性
3. 遍历 ids 执行对应操作，收集结果
4. 支持部分成功场景（返回 207 状态码）

### 测试用例
- [x] 批量 reset 3 个 provider
- [x] 批量 enable/disable
- [x] 批量 delete
- [x] 部分 id 不存在的场景
- [x] 空 ids 数组

### 完成标志
- [x] API 可正常调用
- [x] 返回格式正确
- [x] 部分失败时返回 207

---

## 阶段 2：日志查询增强

### 目标
为 `GET /admin/api/logs` 增加过滤和排序参数。

### API 定义

```
GET /admin/api/logs?provider_id=xxx&success=false&sort_by=latency_ms&sort_order=desc
```

### 新增参数

| 参数 | 类型 | 说明 | 示例 |
|------|------|------|------|
| `provider_id` | string | 按 Provider ID 过滤 | `openai-1` |
| `api_type` | string | 按 API 类型过滤 | `claude`/`codex`/`gemini`/`custom` |
| `success` | bool | 按成功/失败过滤 | `true`/`false` |
| `user_id` | string | 按用户过滤 | `user-123` |
| `start_time` | RFC3339 | 开始时间 | `2026-01-11T00:00:00Z` |
| `end_time` | RFC3339 | 结束时间 | `2026-01-12T00:00:00Z` |
| `min_latency` | int | 最小延迟(ms) | `1000` |
| `sort_by` | string | 排序字段 | `created_at`/`latency_ms` |
| `sort_order` | string | 排序方向 | `asc`/`desc` |

### 实现要点
1. 修改 `internal/store/log_store.go` 的查询方法，支持动态 WHERE 条件
2. 使用参数化查询防止 SQL 注入
3. 修改 handler 解析新参数
4. 默认排序：`created_at DESC`

### 测试用例
- [x] 单个过滤条件
- [x] 多个过滤条件组合
- [x] 时间范围过滤
- [x] 按延迟排序（找慢请求）
- [x] 边界条件（空结果）

### 完成标志
- [x] 所有过滤参数生效
- [x] 排序功能正常
- [x] SQL 注入测试通过（使用参数化查询）

---

## 阶段 3：系统统计摘要 API（基础版）

### 目标
提供系统级统计数据聚合接口，用于 Dashboard 展示。

### API 定义

```
GET /admin/api/stats?period=24h
```

### 参数
| 参数 | 说明 | 可选值 | 默认值 |
|------|------|--------|--------|
| `period` | 统计时间范围 | `24h`/`7d`/`30d`/`all` | `24h` |

### 响应示例
```json
{
  "total_requests": 12580,
  "success_count": 12100,
  "fail_count": 480,
  "success_rate": 0.9618,
  "avg_latency_ms": 1250,
  "providers": {
    "total": 5,
    "healthy": 4,
    "unhealthy": 1,
    "disabled": 0
  },
  "requests_by_api_type": {
    "claude": 5000,
    "codex": 4000,
    "gemini": 3580
  },
  "requests_by_provider": [
    {"id": "openai-1", "name": "OpenAI Primary", "count": 3000, "success_rate": 0.98},
    {"id": "claude-1", "name": "Claude Main", "count": 5000, "success_rate": 0.95}
  ],
  "time_range": {
    "start": "2026-01-11T00:00:00Z",
    "end": "2026-01-12T00:00:00Z"
  }
}
```

### 实现要点
1. 在 `internal/admin/handlers.go` 添加 `GetStats` handler
2. 在 `internal/store/` 添加统计查询方法
3. 使用 SQL 聚合函数：`COUNT`, `AVG`, `SUM`
4. Provider 状态从 HealthManager 获取

### 测试用例
- [ ] 不同 period 参数
- [ ] 无数据时返回零值
- [ ] 统计数据准确性验证

### 完成标志
- [ ] API 返回完整统计数据
- [ ] 数据计算正确
- [ ] 响应时间 < 500ms

---

## 阶段 4：统计 API 增强（时间序列）

### 目标
在阶段3基础上，增加时间粒度聚合，用于绘制趋势图。

### API 定义

```
GET /admin/api/stats?period=24h&granularity=1h
```

### 新增参数
| 参数 | 说明 | 可选值 |
|------|------|--------|
| `granularity` | 时间粒度 | `5m`/`15m`/`1h`/`6h`/`1d` |

### 响应示例（增加 timeseries 字段）
```json
{
  "summary": {
    "total_requests": 12580,
    "success_rate": 0.9618,
    ...
  },
  "timeseries": [
    {
      "time": "2026-01-12T00:00:00Z",
      "requests": 500,
      "success_count": 490,
      "fail_count": 10,
      "success_rate": 0.98,
      "avg_latency_ms": 1100
    },
    {
      "time": "2026-01-12T01:00:00Z",
      "requests": 480,
      "success_count": 465,
      "fail_count": 15,
      "success_rate": 0.97,
      "avg_latency_ms": 1250
    }
  ]
}
```

### 实现要点
1. 使用 SQL 的 `strftime` 或时间函数按粒度分组
2. 补齐没有数据的时间点（填充零值）
3. 考虑查询性能，大时间范围 + 小粒度时返回错误

### 粒度限制规则
| period | 允许的最小粒度 |
|--------|---------------|
| 24h | 5m |
| 7d | 1h |
| 30d | 6h |
| all | 1d |

### 完成标志
- [ ] 时间序列数据正确
- [ ] 时间点连续（补零）
- [ ] 粒度限制生效

---

## 阶段 5：Group 快捷操作 API

### 目标
为 Group 提供语义化的启用/禁用快捷接口。

### API 定义

```
POST /admin/api/groups/{id}/enable
POST /admin/api/groups/{id}/disable
```

**响应**：
```json
{
  "success": true,
  "group": {
    "id": "group-1",
    "name": "Primary Group",
    "enabled": true
  }
}
```

### 实现要点
1. 在 `internal/admin/handlers.go` 添加 `EnableGroup` 和 `DisableGroup` handler
2. 复用现有的 Group 更新逻辑
3. 返回更新后的 Group 状态

### 完成标志
- [ ] enable/disable 接口可用
- [ ] 返回更新后状态
- [ ] 404 处理正确

---

## 阶段 6：配置导出/导入 API

### 目标
支持配置的备份恢复和环境迁移。

### API 定义

**导出**：
```
GET /admin/api/config/export
```

响应：
```json
{
  "version": "1.0",
  "exported_at": "2026-01-12T10:00:00Z",
  "providers": [...],
  "groups": [...],
  "settings": {...}
}
```

**导入预览**：
```
POST /admin/api/config/import?dry_run=true
```

响应：
```json
{
  "dry_run": true,
  "changes": {
    "providers": {"add": 2, "update": 1, "delete": 0},
    "groups": {"add": 1, "update": 0, "delete": 0}
  },
  "warnings": ["Provider 'old-api' will be overwritten"]
}
```

**实际导入**：
```
POST /admin/api/config/import
```

响应：
```json
{
  "success": true,
  "applied": {
    "providers": {"added": 2, "updated": 1},
    "groups": {"added": 1, "updated": 0}
  }
}
```

### 实现要点
1. 导出：收集所有 providers、groups 及其配置
2. 导入：支持 dry_run 模式预览变更
3. 冲突处理：ID 相同时覆盖（可配置策略）
4. 版本兼容：记录 export 版本号

### 测试用例
- [ ] 导出完整配置
- [ ] dry_run 预览
- [ ] 实际导入
- [ ] 冲突覆盖场景
- [ ] 空配置导入

### 完成标志
- [ ] 导出格式正确
- [ ] dry_run 预览准确
- [ ] 导入后数据正确

---

## 📝 执行建议

### 给 AI 的指令模板

每个阶段开始时，可使用以下模板：

```
请实现 API_IMPLEMENTATION_PLAN.md 中的【阶段 X】。

要求：
1. 阅读阶段描述和实现要点
2. 在现有代码基础上添加功能
3. 遵循项目现有的代码风格
4. 完成后标记测试用例状态
5. 更新阶段状态为 ✅ 已完成
```

### 阶段依赖关系

```
阶段 1 (批量操作) ─┬─→ 可并行
阶段 2 (日志增强) ─┘
        │
        ▼
阶段 3 (统计基础) ─→ 阶段 4 (统计增强)
        │
        ▼
阶段 5 (Group操作) ─→ 可独立
        │
        ▼
阶段 6 (导入导出) ─→ 建议最后
```

**说明**：
- 阶段 1-2 无依赖，可按任意顺序或并行
- 阶段 4 依赖阶段 3
- 阶段 5 相对独立
- 阶段 6 建议最后实现（涉及所有数据模型）

