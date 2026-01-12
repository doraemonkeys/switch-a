# API 补充实现计划

---

## 📋 阶段总览

| 阶段 | 功能 | 优先级 | 预估复杂度 | 状态 |
|------|------|--------|------------|------|
| 1 | Provider 批量操作 API | 🔴高 | ⭐⭐ | ✅ 已完成 |
| 2 | 日志查询增强（过滤+排序） | 🔴高 | ⭐⭐⭐ | ✅ 已完成 |
| 3 | 系统统计摘要 API（基础版） | 🔴高 | ⭐⭐⭐ | ✅ 已完成 |
| 4 | 统计 API 增强（时间序列） | 🟡中 | ⭐⭐⭐ | ✅ 已完成 |
| 5 | Group 快捷操作 API | 🟡中 | ⭐ | ✅ 已完成 |
| 6 | 配置导出/导入 API | 🟡中 | ⭐⭐⭐ | ⬜ 待开始 |

---

## ✅ 已完成阶段摘要

### 阶段 1：Provider 批量操作 API
- **端点**: `POST /admin/api/providers/batch`
- **功能**: 批量 reset/enable/disable/delete 操作
- **请求**: `{"action": "reset", "ids": ["id1", "id2"]}`
- **响应**: 200 全部成功 / 207 部分失败

### 阶段 2：日志查询增强
- **端点**: `GET /admin/api/logs`
- **新增参数**: `provider_id`, `api_type`, `success`, `user_id`, `start_time`, `end_time`, `min_latency`, `sort_by`, `sort_order`
- **默认排序**: `created_at DESC`

### 阶段 3：系统统计摘要 API
- **端点**: `GET /admin/api/stats?period=24h`
- **period 可选值**: `24h`/`7d`/`30d`/`all`
- **返回**: total_requests, success_rate, avg_latency_ms, providers 状态, requests_by_api_type, requests_by_provider

### 阶段 4：统计 API 增强（时间序列）
- **端点**: `GET /admin/api/stats?period=24h&granularity=1h`
- **granularity 可选值**: `5m`/`15m`/`1h`/`6h`/`1d`
- **粒度限制**: 24h≥5m, 7d≥1h, 30d≥6h, all≥1d
- **新增返回字段**: `timeseries` 数组，包含每个时间桶的 requests/success_count/fail_count/success_rate/avg_latency_ms

### 阶段 5：Group 快捷操作 API
- **端点**: `POST /admin/api/groups/{id}/enable` 和 `POST /admin/api/groups/{id}/disable`
- **功能**: 为 Group 提供语义化的启用/禁用快捷接口
- **响应**: 返回更新后的 Group 对象（含 id, name, enabled 等字段）

---

## 阶段 5：Group 快捷操作 API（详细）

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
- [x] enable/disable 接口可用
- [x] 返回更新后状态
- [x] 404 处理正确

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

## 阶段依赖关系

```
阶段 1-4 ✅ 已完成
        │
        ▼
阶段 5 (Group操作) ─→ 可独立
        │
        ▼
阶段 6 (导入导出) ─→ 建议最后
```
