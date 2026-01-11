# Switch-A 实现计划

> AI 供应商代理池服务 - 自动切换可用供应商

---

## 总体进度 Checklist

### Phase 1-4: 后端核心 ✅ 已完成
- [x] 工程骨架 (Go module、配置、日志、GORM+SQLite)
- [x] 代理转发核心 (路由、Header透传、SSE流式、请求体缓冲)
- [x] 供应商选择与健康管理 (策略选择、并发限制、粘性会话、熔断器)
- [x] 管理 API (认证中间件、Provider/Group/Config/Health/Logs CRUD)

> 代码位置: `internal/config/`, `internal/logger/`, `internal/store/`, `internal/server/`, `internal/proxy/`, `internal/selector/`, `internal/health/`, `internal/admin/`

### Phase 5: React 管理界面 (预计 3-5 天)
- [ ] React + TypeScript + Vite 项目初始化
- [ ] Dashboard 页面 (状态总览)
- [ ] 供应商管理页面
- [ ] 分组管理页面
- [ ] 配置编辑页面
- [ ] 日志查看页面
- [ ] 静态资源嵌入 Go 二进制

### Phase 6: 完善与发布 (预计 1-2 天)
- [ ] 日志清理策略
- [ ] 优雅关闭
- [ ] README 文档
- [ ] 构建脚本 (Windows/Linux)
- [ ] Docker 配置

---

## 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                          Client                                  │
└─────────────────────┬───────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                     HTTP Server (Go)                             │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │  POST /v1/messages              → Claude API Proxy          ││
│  │  GET  /v1/models                → Models List (Claude)      ││
│  │  POST /responses                → Codex API Proxy           ││
│  │  POST /gemini/v1beta/*, /v1/*   → Gemini API Proxy          ││
│  │  POST /custom/:toolId/v1/messages → Custom CLI Proxy        ││
│  │  GET  /custom/:toolId/v1/models → Custom Models List        ││
│  │  ────────────────────────────────────────────────────────   ││
│  │  /admin/api/*                   → Management API (需认证)   ││
│  │  /admin/*                       → Frontend Static (无需认证)││
│  │  /health                        → 健康检查                  ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│  Core Services: Proxy | Selector | Health | Sticky | Store      │
└─────────────────────────────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│  SQLite: providers, groups, health_states, runtime_config, logs │
└─────────────────────────────────────────────────────────────────┘
```

---

## 数据模型参考

### Provider
```go
type Provider struct {
    ID, Name, BaseURL, APIKey, AuthMode string
    APITypes []ProviderAPIType  // claude/codex/gemini/custom:xxx
    GroupID *string
    Weight, Priority, Concurrency, MaxRetries int
    Enabled bool
}
```

### Group
```go
type Group struct {
    ID, Name, Strategy string  // strategy: priority/random/weight
    Priority, Weight int
    Enabled bool
    Providers []Provider
}
```

### 组策略说明
- **组间选择** (`inter_group_strategy`): priority/weight/random
- **组内选择** (`Group.Strategy`): priority/weight/random
- 无分组供应商视为独立虚拟组，优先级最低

---

## 配置参考

### 启动配置 (环境变量)
| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `SWITCHA_PORT` | 监听端口 | `8080` |
| `SWITCHA_DB_PATH` | SQLite 文件路径 | `./data.db` |
| `SWITCHA_ADMIN_TOKEN` | 管理界面认证令牌 | **必填** |

### 运行时配置 (数据库)
`auth_mode`, `user_header`, `trust_proxy_headers`, `upstream_connect_timeout`, `upstream_read_timeout`, `sticky_enabled`, `sticky_ttl`, `circuit_failure`, `circuit_window`, `circuit_disable`, `max_body_size`, `max_retries`, `log_retention_days`, `inter_group_strategy`

---

## API 参考

### 代理 API (无需认证)
| 路径 | 说明 |
|------|------|
| `POST /v1/messages` | Claude API |
| `GET /v1/models` | 模型列表 |
| `POST /responses` | Codex API |
| `POST /gemini/v1beta/*`, `/gemini/v1/*` | Gemini API |
| `POST /custom/:toolId/v1/messages` | 自定义工具 |
| `GET /health` | 健康检查 |

### 管理 API (需 `Authorization: Bearer <token>`)
- `/admin/api/providers` - 供应商 CRUD + enable/disable/reset
- `/admin/api/groups` - 分组 CRUD
- `/admin/api/config` - 运行时配置
- `/admin/api/health` - 健康状态
- `/admin/api/logs` - 请求日志
- `/admin/api/status` - 系统状态

### 错误码
- 代理错误: `UNKNOWN_API_TYPE`, `PROVIDER_UNAVAILABLE`, `PROVIDER_EXHAUSTED`, `BODY_TOO_LARGE`, `GATEWAY_TIMEOUT`
- 管理错误: `UNAUTHORIZED`, `NOT_FOUND`, `VALIDATION_ERROR`, `CONFLICT`

---

## Phase 5: React 管理界面

**任务清单**：

1. **项目初始化** (`web/`)
   - React 19 + TypeScript + Vite
   - Tailwind CSS 或其他 UI 库
   - API 客户端封装

2. **Dashboard 页面**
   - 供应商状态卡片
   - 成功率统计
   - 最近错误

3. **供应商管理页面**
   - 列表展示 (状态、成功率)
   - 新增/编辑表单
   - 启用/禁用/重置操作

4. **分组管理页面**
   - 分组列表
   - 新增/编辑
   - 组内供应商管理

5. **配置页面**
   - 运行时配置表单
   - 保存/重置

6. **日志页面**
   - 分页列表
   - 筛选功能

7. **嵌入部署**
   - `go:embed` 嵌入 dist
   - SPA history fallback

**验收标准**：
- 单文件运行，访问 `/admin` 可见界面
- 所有功能可通过界面操作
- 响应式布局

---

## Phase 6: 完善与发布

**任务清单**：

1. **日志清理** - 定时任务按 `log_retention_days` 清理
2. **优雅关闭** - 信号监听、等待请求完成、关闭数据库
3. **文档** - README (部署、配置、使用)
4. **构建脚本** - Windows/Linux/macOS (goreleaser 或 Makefile)
5. **Docker** - Dockerfile + docker-compose

---

## 部署说明

```bash
# 构建前端
cd web && npm install && npm run build && cd ..

# 构建后端 (嵌入前端)
go build -o switch-a ./cmd/switch-a

# 运行
SWITCHA_PORT=8080 \
SWITCHA_DB_PATH=./data.db \
SWITCHA_ADMIN_TOKEN=your-secure-token \
./switch-a
```
