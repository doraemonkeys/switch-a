# Switch-A 实现计划

> AI 供应商代理池服务 - 自动切换可用供应商

---

## 总体进度 Checklist

### Phase 1: 工程骨架 (预计 1-2 天) ✅
- [x] Go module 初始化 + 目录结构
- [x] 启动配置加载 (环境变量)
- [x] 日志初始化 (doraemonkeys/mylog/zap)
- [x] 核心接口定义 (Store, Selector, Clock, HTTPDoer)
- [x] GORM + SQLite 初始化 (AutoMigrate)
- [x] 健康检查端点 `/health`
- [x] `make verify` 基础通过

### Phase 2: 代理转发核心 (预计 2-3 天)
- [ ] API 类型路由解析 (claude/codex/gemini/custom)
- [ ] 请求信息提取 (IP, User, Model)
- [ ] Header 透传与过滤
- [ ] 认证头处理 (auto/bearer/x-api-key)
- [ ] HTTP 转发 (普通响应)
- [ ] SSE 流式响应代理
- [ ] 请求体缓冲 (支持重试)

### Phase 3: 供应商选择与健康管理 (预计 2-4 天)
- [ ] 供应商/分组数据模型 + 存储
- [ ] 选择策略实现 (优先级/随机/权重)
- [ ] 并发限制实现 (选择时检查)
- [ ] 粘性会话缓存
- [ ] 失败判定逻辑 (5xx/429/超时)
- [ ] 滑动窗口熔断器
- [ ] 失败重试 (写入响应前)
- [ ] 自动恢复机制

### Phase 4: 管理 API (预计 2-3 天)
- [ ] 管理端认证中间件
- [ ] 供应商 CRUD API
- [ ] 分组 CRUD API
- [ ] 运行时配置 API
- [ ] 健康状态查询 API
- [ ] 请求日志 API
- [ ] 手动启用/禁用/恢复 API

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

**总计预估: 11-19 天**

---

## 一、项目概述

### 1.1 核心功能

- **代理转发**：统一入口代理多个 AI 供应商，自动切换可用服务
- **粘性会话**：基于 `ip + user + api_type` 缓存成功供应商，利用上游对话缓存
- **分组策略**：供应商分组管理，支持优先级/随机/权重策略
- **健康管理**：自动检测故障、熔断禁用、自动/手动恢复
- **管理界面**：状态展示、配置编辑、手动控制

### 1.2 技术栈

| 层级 | 技术选型 |
|------|----------|
| 后端 | Go 1.24+ |
| HTTP 框架 | 标准库 `net/http` + `http.ServeMux` (Go 1.22+ 增强路由) |
| ORM | GORM (gorm.io/gorm + gorm.io/driver/sqlite) |
| 日志 | doraemonkeys/mylog/zap + go.uber.org/zap |
| 前端 | React 19 + TypeScript + Vite |
| 数据库 | SQLite (嵌入式) |
| 部署 | 单二进制 (embed 前端资源) |

### 1.3 工程约束

遵循 `docs/ENGINEERING_GUIDELINES.md`：
- 可测试性：依赖注入，接口抽象
- 避免全局状态：不使用单例
- Go 规范：不在 struct 中存储 context
- 质量门槛：lint、覆盖率 >=90%、sloc 限制

---

## 二、系统架构

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
└─────────────────────┬───────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Core Services                                │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐       │
│  │   Proxy       │  │   Selector    │  │   Health      │       │
│  │   (转发代理)   │  │   (供应商选择) │  │   (熔断管理)   │       │
│  └───────────────┘  └───────────────┘  └───────────────┘       │
│  ┌───────────────┐  ┌───────────────┐                          │
│  │   Sticky      │  │   Store       │                          │
│  │   (粘性缓存)   │  │   (数据存储)   │                          │
│  └───────────────┘  └───────────────┘                          │
└─────────────────────────────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                     SQLite (data.db)                             │
│  - providers      (供应商配置)                                   │
│  - groups         (分组配置)                                     │
│  - health_states  (健康状态)                                     │
│  - runtime_config (运行时配置)                                   │
│  - request_logs   (请求日志)                                     │
└─────────────────────────────────────────────────────────────────┘
```

---

## 三、目录结构

```
switch-a/
├── cmd/
│   └── switch-a/
│       └── main.go              # 程序入口 (仅依赖装配)
├── internal/
│   ├── config/
│   │   └── config.go            # 配置结构与加载
│   ├── logger/
│   │   └── logger.go            # 日志初始化 (doraemonkeys/mylog/zap)
│   ├── server/
│   │   ├── server.go            # HTTP 服务器
│   │   └── middleware.go        # 中间件 (日志、恢复、认证)
│   ├── proxy/
│   │   ├── handler.go           # 代理请求处理器
│   │   ├── router.go            # API 类型路由解析
│   │   ├── extractor.go         # 请求信息提取 (IP, User, Model)
│   │   ├── headers.go           # Header 透传与过滤
│   │   └── transport.go         # HTTP 传输 (含 SSE)
│   ├── selector/
│   │   ├── selector.go          # 供应商选择器
│   │   ├── strategy.go          # 选择策略 (优先级/随机/权重)
│   │   └── sticky.go            # 粘性会话缓存
│   ├── health/
│   │   ├── manager.go           # 健康状态管理
│   │   └── circuit.go           # 熔断器 (滑动窗口)
│   ├── store/
│   │   ├── store.go             # 存储接口定义
│   │   └── sqlite.go            # GORM SQLite 实现 (含 AutoMigrate)
│   ├── admin/
│   │   ├── handler.go           # 管理 API 处理器
│   │   └── auth.go              # 管理认证
│   └── model/
│       └── model.go             # 数据模型定义
├── web/                         # React 前端
│   ├── src/
│   │   ├── components/
│   │   ├── pages/
│   │   ├── api/
│   │   └── App.tsx
│   ├── package.json
│   └── vite.config.ts
├── docs/
│   ├── PLAN.md                  # 本文档
│   └── ENGINEERING_GUIDELINES.md
├── Makefile
├── go.mod
└── go.sum
```

---

## 四、数据模型

### 4.1 供应商 (Provider)

```go
type Provider struct {
    ID          string    `gorm:"primaryKey" json:"id"`
    Name        string    `gorm:"not null" json:"name"`
    BaseURL     string    `gorm:"not null" json:"base_url"`
    APIKey      string    `gorm:"not null" json:"api_key"`      // 明文存储
    APITypes    []ProviderAPIType `gorm:"foreignKey:ProviderID" json:"api_types"` // 通过关联表存储
    AuthMode    string    `gorm:"default:auto" json:"auth_mode"`    // auto/bearer/x-api-key
    GroupID     *string   `gorm:"index" json:"group_id"`
    Group       *Group    `gorm:"foreignKey:GroupID" json:"-"`
    Weight      int       `gorm:"default:1" json:"weight"`       // 1-100
    Priority    int       `gorm:"default:0" json:"priority"`     // 越小越优先
    Concurrency int       `gorm:"default:0" json:"concurrency"`  // 最大并发 (0=不限)
    MaxRetries  int       `gorm:"default:-1" json:"max_retries"` // -1=使用全局默认值, 0=不重试
    Enabled     bool      `gorm:"default:true;index" json:"enabled"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// ProviderAPIType 供应商-API类型关联表
type ProviderAPIType struct {
    ProviderID string `gorm:"primaryKey" json:"provider_id"`
    APIType    string `gorm:"primaryKey;index" json:"api_type"` // claude/codex/gemini/custom:xxx
}
```

> **注意**: `APITypes` 通过 GORM 的 `has many` 关联自动管理，
> 使用 `Preload("APITypes")` 加载关联数据。

### 4.2 分组 (Group)

```go
type Group struct {
    ID        string     `gorm:"primaryKey" json:"id"`
    Name      string     `gorm:"not null" json:"name"`
    Strategy  string     `gorm:"default:priority" json:"strategy"`   // priority/random/weight
    Priority  int        `gorm:"default:0" json:"priority"`   // 组间优先级
    Weight    int        `gorm:"default:1" json:"weight"`     // 组间权重
    Enabled   bool       `gorm:"default:true" json:"enabled"`
    Providers []Provider `gorm:"foreignKey:GroupID" json:"providers,omitempty"`
    CreatedAt time.Time  `json:"created_at"`
    UpdatedAt time.Time  `json:"updated_at"`
}
```

> **组策略说明**：
> 
> 选择供应商分为**两层**：组间选择 + 组内选择
> 
> | 配置项 | 位置 | 用途 |
> |--------|------|------|
> | `inter_group_strategy` | runtime_config | 决定用什么**算法**选择分组 |
> | `Group.Priority` | Group struct | 该组在组间选择中的**优先级属性** |
> | `Group.Weight` | Group struct | 该组在组间选择中的**权重属性** |
> | `Group.Strategy` | Group struct | 组内选择供应商的**算法** |
> 
> **组间选择策略**（`inter_group_strategy`）：
> - `priority`：按 `Group.Priority` 值从小到大排序，优先选择 Priority 最小的分组
> - `weight`：按 `Group.Weight` 值进行加权随机选择
> - `random`：完全随机选择分组（忽略 Priority 和 Weight）
> 
> **组内选择策略**（`Group.Strategy`）：
> - `priority`：按 `Provider.Priority` 值从小到大排序
> - `weight`：按 `Provider.Weight` 值进行加权随机
> - `random`：完全随机选择

### 4.3 健康状态 (HealthState)

```go
type HealthState struct {
    ProviderID     string     `gorm:"primaryKey" json:"provider_id"`
    Provider       *Provider  `gorm:"foreignKey:ProviderID;constraint:OnDelete:CASCADE" json:"-"`
    Available      bool       `gorm:"default:true" json:"available"`
    SuccessCount   int64      `gorm:"default:0" json:"success_count"`
    FailCount      int64      `gorm:"default:0" json:"fail_count"`
    LastSuccess    *time.Time `json:"last_success"`
    LastFailure    *time.Time `json:"last_failure"`
    LastError      string     `json:"last_error"`
    DisabledUntil  *time.Time `json:"disabled_until"`
    DisabledReason string     `json:"disabled_reason"`
}
```

### 4.4 粘性缓存

```go
type StickyKey struct {
    IP      string
    User    string
    APIType string
}

type StickyEntry struct {
    ProviderID string
    ExpiresAt  time.Time
}
```

---

## 五、配置设计

### 5.1 启动配置 (环境变量)

仅 3 个环境变量，启动时读取一次：

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `SWITCHA_PORT` | 监听端口 | `8080` |
| `SWITCHA_DB_PATH` | SQLite 文件路径 | `./data.db` |
| `SWITCHA_ADMIN_TOKEN` | 管理界面认证令牌 | **必填** |

### 5.2 运行时配置 (数据库存储)

通过管理界面修改，无需重启：

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `auth_mode` | 全局认证模式 (auto/bearer/x-api-key)，仅当 Provider.AuthMode 为空时使用 | `auto` |
| `user_header` | 用户ID请求头名称 | `X-User-ID` |
| `trust_proxy_headers` | 是否信任 X-Forwarded-For | `true` |
| `upstream_connect_timeout` | 上游连接超时 (秒) | `10` |
| `upstream_read_timeout` | 上游响应超时 (秒)，0=不限制 (适用于长时间生成的 SSE) | `0` |
| `sticky_enabled` | 是否启用粘性会话 | `true` |
| `sticky_ttl` | 粘性时长 (秒) | `300` |
| `circuit_failure` | 熔断失败阈值 | `3` |
| `circuit_window` | 熔断检测窗口 (秒) | `60` |
| `circuit_disable` | 熔断禁用时长 (秒) | `300` |
| `max_body_size` | 最大请求体 (MB) | `10` |
| `max_retries` | 全局最大重试次数 (供应商级别可覆盖) | `3` |
| `log_retention_days` | 日志保留天数 | `7` |
| `inter_group_strategy` | 组间选择策略 | `priority` |

**默认值初始化**：

首次启动时，自动将上述默认值写入 `runtime_config` 表（仅当 key 不存在时）：

```go
// internal/store/sqlite.go
func (s *SQLiteStore) InitDefaultConfig(ctx context.Context) error {
    defaults := map[string]string{
        "auth_mode":                "auto",
        "user_header":              "X-User-ID",
        "trust_proxy_headers":      "true",
        "upstream_connect_timeout": "10",
        "upstream_read_timeout":    "0",
        "sticky_enabled":           "true",
        "sticky_ttl":               "300",
        "circuit_failure":          "3",
        "circuit_window":           "60",
        "circuit_disable":          "300",
        "max_body_size":            "10",
        "max_retries":              "3",
        "log_retention_days":       "7",
        "inter_group_strategy":     "priority",
    }
    for key, value := range defaults {
        // INSERT OR IGNORE: 仅当 key 不存在时插入
        err := s.db.WithContext(ctx).Exec(
            "INSERT OR IGNORE INTO runtime_configs (key, value, updated_at) VALUES (?, ?, ?)",
            key, value, time.Now(),
        ).Error
        if err != nil {
            return err
        }
    }
    return nil
}
```

> 在 `main.go` 中 AutoMigrate 后调用 `store.InitDefaultConfig(ctx)`

### 5.3 日志配置

使用 `doraemonkeys/mylog/zap` 封装的 zap 日志库，支持灵活的 Builder 配置：

```go
package main

import (
	mylog "github.com/doraemonkeys/mylog/zap"
	"go.uber.org/zap"
)

func main() {
	logger := mylog.NewBuilder().Build()
}
```

**Builder 配置方法**：

```go
**推荐配置**：

```go
// 生产环境配置
logger := mylog.NewBuilder().
	LogPath("./logs/switch-a.log").
	Level(zapcore.InfoLevel).
	MaxLogSize(100).
	MaxKeepDays(7).
	JSONFormatFile().
	Build()

// 开发环境配置
logger := mylog.NewBuilder().
	NoLogFile().
	Level(zapcore.DebugLevel).
	Build()
```

---

## 六、数据库 Schema (GORM)

使用 GORM 的 `AutoMigrate` 自动管理数据库 Schema，无需手动编写 SQL。

### 6.1 GORM 模型定义

```go
// RuntimeConfig 运行时配置表
type RuntimeConfig struct {
    Key       string    `gorm:"primaryKey" json:"key"`
    Value     string    `gorm:"not null" json:"value"`
    UpdatedAt time.Time `json:"updated_at"`
}

// RequestLog 请求日志表
type RequestLog struct {
    ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
    ProviderID string     `gorm:"index" json:"provider_id"`
    APIType    string     `json:"api_type"`
    Model      string     `json:"model"`
    ClientIP   string     `json:"client_ip"`
    UserID     string     `json:"user_id"`
    StatusCode int        `json:"status_code"`
    LatencyMs  int64      `json:"latency_ms"`
    Success    bool       `json:"success"`
    ErrorMsg   string     `json:"error_msg"`
    CreatedAt  time.Time  `gorm:"index" json:"created_at"`
}
```

> 其他模型定义见 4.1-4.3 节

### 6.2 数据库初始化

```go
package store

import (
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

func NewDB(dbPath string) (*gorm.DB, error) {
    db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Warn),
    })
    if err != nil {
        return nil, err
    }

    // 启用 WAL 模式提升并发性能
    db.Exec("PRAGMA journal_mode=WAL")
    
    // 自动迁移
    err = db.AutoMigrate(
        &Group{},
        &Provider{},
        &ProviderAPIType{},
        &HealthState{},
        &RuntimeConfig{},
        &RequestLog{},
    )
    if err != nil {
        return nil, err
    }

    return db, nil
}
```

### 6.3 API 类型说明

| API 类型 | 匹配路径 | 示例 |
|----------|----------|------|
| `claude` | `/v1/messages/*` | 标准 Claude API |
| `codex` | `/responses/*` | Codex API |
| `gemini` | `/gemini/*` | Gemini API |
| `custom:xxx` | `/custom/xxx/*` | 自定义 CLI 工具 |

**Custom API 类型配置**：
- 存储格式: 精确匹配字符串，如 `"custom:mytool"`, `"custom:search"`
- 不支持通配符: 必须完整指定 toolId
- 管理界面: 直接输入 `"custom:toolId"` 格式的字符串

---

## 七、核心接口

### 7.1 存储接口

```go
type Store interface {
    // Provider
    ListProviders(ctx context.Context) ([]Provider, error)
    // ListProvidersByAPIType 通过 GORM Joins 查询关联表
    ListProvidersByAPIType(ctx context.Context, apiType string) ([]Provider, error)
    GetProvider(ctx context.Context, id string) (*Provider, error)
    // CreateProvider 使用 GORM 处理关联表
    CreateProvider(ctx context.Context, p *Provider) error
    // UpdateProvider 使用 GORM 更新关联
    UpdateProvider(ctx context.Context, p *Provider) error
    // DeleteProvider GORM 自动处理 CASCADE 删除
    DeleteProvider(ctx context.Context, id string) error

    // Group
    ListGroups(ctx context.Context) ([]Group, error)
    GetGroup(ctx context.Context, id string) (*Group, error)
    CreateGroup(ctx context.Context, g *Group) error
    UpdateGroup(ctx context.Context, g *Group) error
    DeleteGroup(ctx context.Context, id string) error

    // Health
    GetHealthState(ctx context.Context, providerID string) (*HealthState, error)
    UpdateHealthState(ctx context.Context, state *HealthState) error
    ListHealthStates(ctx context.Context) ([]HealthState, error)

    // Config
    GetConfig(ctx context.Context, key string) (string, error)
    GetAllConfig(ctx context.Context) (map[string]string, error)
    SetConfig(ctx context.Context, key, value string) error

    // Logs
    InsertLog(ctx context.Context, log *RequestLog) error
    ListLogs(ctx context.Context, limit, offset int) ([]RequestLog, error)
    CleanOldLogs(ctx context.Context, beforeDays int) error
}
```



### 7.2 选择器接口

```go
type Selector interface {
    // Select 选择一个可用的供应商
    Select(ctx context.Context, req *SelectRequest) (*Provider, error)
}

type SelectRequest struct {
    ClientIP string
    User     string
    APIType  string
    Model    string  // 仅用于日志，不参与选择
}
```

### 7.3 健康管理接口

```go
type HealthManager interface {
    // MarkSuccess 标记成功
    MarkSuccess(ctx context.Context, providerID string)
    // MarkFailure 标记失败，返回是否触发熔断
    MarkFailure(ctx context.Context, providerID string, err error) bool
    // IsAvailable 检查是否可用
    IsAvailable(ctx context.Context, providerID string) bool
    // ManualDisable 手动禁用
    ManualDisable(ctx context.Context, providerID string, reason string) error
    // ManualEnable 手动启用 (解除禁用)
    ManualEnable(ctx context.Context, providerID string) error
}
```

### 7.4 粘性缓存接口

```go
type StickyCache interface {
    Get(key StickyKey) (providerID string, found bool)
    Set(key StickyKey, providerID string, ttl time.Duration)
    Delete(key StickyKey)
}
```

---

## 八、API 设计

### 8.0 错误响应处理

#### 8.0.1 代理 API 错误处理

代理 API 的错误分两种来源，处理策略不同：

**1. 上游错误 (来自 AI 厂商) → 原样透传**

当上游供应商返回错误响应时，**原样透传**给客户端：
- 透传 HTTP 状态码
- 透传响应 Headers
- 透传响应 Body（保持原始 JSON 格式）

这确保客户端（如 Cursor、Claude Desktop）能正确解析厂商特定的错误格式：

```
Claude:  {"type": "error", "error": {"type": "invalid_request_error", "message": "..."}}
OpenAI:  {"error": {"code": "invalid_api_key", "message": "...", "type": "..."}}
Gemini:  {"error": {"code": 400, "message": "...", "status": "INVALID_ARGUMENT"}}
```

**2. 网关自身错误 → 统一格式**

仅当错误由 Switch-A 网关本身产生时，使用统一格式：

```go
type GatewayError struct {
    Error struct {
        Code    string `json:"code"`    // 错误码
        Message string `json:"message"` // 错误描述
    } `json:"error"`
}
```

| 错误码 | HTTP 状态 | 触发场景 |
|--------|-----------|----------|
| `UNKNOWN_API_TYPE` | 400 | 无法识别的 API 路径（记录警告日志） |
| `PROVIDER_UNAVAILABLE` | 503 | 无可用供应商（全部熔断/禁用/无匹配） |
| `PROVIDER_EXHAUSTED` | 503 | 所有供应商均尝试失败（重试耗尽） |
| `BODY_TOO_LARGE` | 413 | 请求体超过 `max_body_size` 配置 |
| `GATEWAY_TIMEOUT` | 504 | 连接上游超时（非上游响应超时） |
| `INTERNAL_ERROR` | 500 | 网关内部错误 |

**网关错误响应示例**：

```json
{
    "error": {
        "code": "PROVIDER_UNAVAILABLE",
        "message": "No available provider for api_type: claude"
    }
}
```

> **设计原则**：网关尽可能透明，只有网关层面的问题才返回网关格式的错误。

#### 8.0.2 管理 API 错误处理

管理 API 使用统一的错误格式：

```go
type ErrorResponse struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}
```

| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| `UNAUTHORIZED` | 401 | 认证失败 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `VALIDATION_ERROR` | 400 | 请求参数校验失败 |
| `CONFLICT` | 409 | 资源冲突（如 ID 重复） |
| `INTERNAL_ERROR` | 500 | 内部服务器错误 |

**管理 API 错误响应示例**：

```json
{
    "code": "NOT_FOUND",
    "message": "Provider not found: provider-123"
}
```

### 8.1 代理 API (无需认证)

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/messages` | Claude API 代理 |
| GET | `/v1/models` | 模型列表 (OpenAI-compatible，路由到 Claude) |
| POST | `/responses` | Codex API 代理 |
| POST | `/gemini/v1beta/*` | Gemini API 代理 (v1beta) |
| POST | `/gemini/v1/*` | Gemini API 代理 (v1) |
| POST | `/custom/:toolId/v1/messages` | 自定义 CLI 工具代理 |
| GET | `/custom/:toolId/v1/models` | 自定义工具模型列表 |
| GET | `/health` | 健康检查 |

### 8.2 管理 API (需认证)

认证方式：`Authorization: Bearer <admin_token>`

#### 供应商管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/api/providers` | 列出所有供应商 |
| POST | `/admin/api/providers` | 创建供应商 |
| GET | `/admin/api/providers/:id` | 获取供应商详情 |
| PUT | `/admin/api/providers/:id` | 更新供应商 |
| DELETE | `/admin/api/providers/:id` | 删除供应商 |
| POST | `/admin/api/providers/:id/enable` | 启用供应商 |
| POST | `/admin/api/providers/:id/disable` | 禁用供应商 |
| POST | `/admin/api/providers/:id/reset` | 解除自动禁用 |

#### 分组管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/api/groups` | 列出所有分组 |
| POST | `/admin/api/groups` | 创建分组 |
| GET | `/admin/api/groups/:id` | 获取分组详情 |
| PUT | `/admin/api/groups/:id` | 更新分组 |
| DELETE | `/admin/api/groups/:id` | 删除分组 |

#### 状态与配置

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/api/status` | 系统状态总览（含各供应商当前并发数） |
| GET | `/admin/api/health` | 所有供应商健康状态 |
| GET | `/admin/api/config` | 获取运行时配置 |
| PUT | `/admin/api/config` | 更新运行时配置 |
| GET | `/admin/api/logs` | 获取请求日志 |

### 8.3 前端静态资源 (无需认证)

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin` | 管理界面入口 |
| GET | `/admin/*` | 前端静态资源 (SPA fallback) |

> **认证策略**：
> 
> 静态资源（HTML/JS/CSS）**不需要认证**，仅管理 API (`/admin/api/*`) 需要认证。
> 
> **原因**：SPA 架构下，HTML/JS 文件无法携带 Bearer Token 头。
> 
> **认证流程**：
> 1. 用户访问 `/admin` → 浏览器加载 SPA（无需认证）
> 2. SPA 调用 `/admin/api/*` → 返回 401 → 前端显示 Token 输入框
> 3. 用户输入 Admin Token → 前端存储到 localStorage
> 4. 后续 API 请求携带 `Authorization: Bearer <token>` 头

---

## 九、核心流程

### 9.1 代理请求流程

```
1. 接收请求
   ↓
2. 解析 API 类型 (根据 URL 路径)
   - POST /v1/messages → claude
   - GET /v1/models → claude (模型列表)
   - POST /responses → codex
   - POST /gemini/v1beta/*, /gemini/v1/* → gemini
   - POST /custom/:toolId/v1/messages → custom:{toolId}
   - GET /custom/:toolId/v1/models → custom:{toolId} (模型列表)
   - 其他 → 记录警告日志，返回 400 错误 (UNKNOWN_API_TYPE)
   ↓
3. 提取请求信息
   - IP: X-Forwarded-For (可配) / X-Real-IP / RemoteAddr
   - User: 从配置的 user_header 读取
   - Model: 从 JSON body 的 model 字段提取 (仅日志用)
     - 特殊: Gemini API 从 URL 路径提取 (如 /models/gemini-pro:generateContent)
     - **性能优化**: 使用流式 JSON 解析，找到 `model` 字段后立即停止
     - **容错处理**: 提取失败时记录为 "unknown"，不影响请求转发
   ↓
4. 缓冲请求体 (支持重试，限制 max_body_size)
   ↓
5. 查询粘性缓存 (如果启用)
   ├── 命中且供应商可用 → 使用缓存的供应商
   └── 命中但供应商不可用 → **清除该缓存条目**，继续选择
   └── 未命中 → 继续选择
   ↓
6. 选择供应商
   a. 获取匹配 API 类型且 enabled 的供应商
   b. 过滤已熔断的供应商
   c. 过滤并发已满的供应商 (concurrency > 0 且当前并发 >= concurrency)
   d. 按组间策略选择分组
   e. 按组内策略选择供应商
   
   **边界情况处理**：
   - 选中组内所有供应商都不可用 → **自动回退到下一个分组**，直到找到可用供应商或全部尝试完毕
   - `group_id = NULL` 的供应商 → 每个视为**独立的单供应商虚拟组**，Priority=MAX, Weight=1，即优先级最低（多个无分组供应商互不合并，各自独立参与组间选择）
   - 重试范围 → **跨分组重试**，但排除已失败的供应商，重新执行选择流程
   ↓
7. 构建上游请求
   a. 透传 headers (过滤认证头 + hop-by-hop)
   b. 写入认证头 (按 auth_mode)
   c. 设置上游 URL
   ↓
8. 执行请求 (受 upstream_connect_timeout / upstream_read_timeout 控制)
   ├── 成功 → 返回响应，标记成功，更新粘性
   └── 失败 (5xx/429/超时/网络错误)
       ├── 尚未写入响应 且 重试次数 < max_retries → 标记失败，重试下一供应商
       ├── 尚未写入响应 但 重试次数 >= max_retries → 返回 PROVIDER_EXHAUSTED 错误
       └── 已写入响应 → 返回错误，标记失败 (不可重试)
   ↓
9. 记录日志 (异步)
```

### 9.2 SSE 流式响应

```
检测响应 Content-Type
   ├── text/event-stream → SSE 模式
   │   ↓
   │   立即写入响应头给客户端
   │   ↓
   │   循环读取上游数据块
   │   ├── 收到数据 → 立即写入客户端 + Flush
   │   ├── 上游关闭 → 正常结束，标记成功
   │   └── 上游出错 → 返回错误，标记失败 (不重试!)
   │
   └── 其他 → 普通模式
       ↓
       读取完整响应后返回
```

**重要边界**：一旦开始向客户端写入响应（尤其 SSE），上游中途出错只能返回错误，**不能切换供应商重试**，避免混合响应。

### 9.3 熔断流程

```
请求失败
   ↓
记录失败 (provider_id, timestamp)
   ↓
检查滑动窗口: circuit_window 秒内失败 >= circuit_failure 次?
   ├── 否 → 保持可用
   └── 是 → 设置 disabled_until = now + circuit_disable
            设置 disabled_reason = "auto: ..."
   ↓
禁用期间该供应商不参与选择
   ↓
disabled_until 到期后自动恢复
或通过管理 API 手动恢复
```

### 9.4 Header 处理

```
透传规则:
- 复制所有请求头到上游

必须过滤:
- 认证头: Authorization, X-API-Key, x-api-key, Proxy-Authorization
- hop-by-hop: Connection, Keep-Alive, TE, Trailer, Transfer-Encoding, Upgrade

认证头写入 (根据 auth_mode):
- bearer: Authorization: Bearer <api_key>
- x-api-key: x-api-key: <api_key>
- auto: 自动检测客户端认证方式（检测顺序如下）
    1. 检查请求中是否有 Authorization 头 → 使用 "bearer" 方式
    2. 否则检查是否有 x-api-key 头 → 使用 "x-api-key" 方式
    3. 都没有 → 默认使用 "bearer" 方式
    
    注：检测仅用于确定认证方式，原始认证头会被过滤，
    然后用供应商的 API Key 按检测到的方式写入新认证头。
```

---

## 十、分阶段实现详情

### Phase 1: 工程骨架

**目标**：项目可编译运行，通过基础质量检查

**任务清单**：

1. **项目初始化**
   - `go mod init`
   - 创建目录结构
   - 配置 `.gitignore`

2. **配置加载** (`internal/config/`)
   - 读取 3 个环境变量
   - 启动时校验 `SWITCHA_ADMIN_TOKEN` 必填

3. **日志初始化** (`internal/logger/`)
   - 使用 `doraemonkeys/mylog/zap` 初始化 logger
   - 支持开发/生产环境不同配置
   - 日志文件路径: `./logs/switch-a.log`

4. **核心接口定义** (`internal/`)
   - `Store` 接口
   - `Selector` 接口
   - `HealthManager` 接口
   - `Clock` / `HTTPDoer` 辅助接口 (便于测试)

5. **GORM + SQLite 基础** (`internal/store/`)
   - GORM 初始化 (gorm.io/gorm + gorm.io/driver/sqlite)
   - 启用 WAL 模式
   - AutoMigrate 自动建表

6. **HTTP Server** (`internal/server/`)
   - 基础 server 启动/关闭
   - `/health` 端点

7. **构建配置**
   - `Makefile` 完善
   - `make verify` 通过

**验收标准**：
- 程序启动后监听端口
- `/health` 返回 200
- `make verify` 通过

---

### Phase 2: 代理转发核心

**目标**：能够成功代理 Claude API 请求（含 SSE）

**任务清单**：

1. **路由解析** (`internal/proxy/router.go`)
   - URL → api_type 映射
   - 默认 claude 透传

2. **信息提取** (`internal/proxy/extractor.go`)
   - 提取 Client IP (支持代理头)
   - 提取 User ID (从配置的 header)
   - 提取 Model (从 JSON body; Gemini 从 URL 路径提取)
     - 使用 `json.Decoder` 流式解析，仅读取到 `model` 字段即停止
     - 对于大请求体（如带图片），避免解析完整 JSON
     - 最多读取 128KB 数据，超出仍未找到 `model` 字段则返回 "unknown"

3. **Header 处理** (`internal/proxy/headers.go`)
   - 透传逻辑
   - 过滤列表
   - 认证头写入

4. **HTTP 传输** (`internal/proxy/transport.go`)
   - 普通请求转发
   - SSE 流式转发
   - 请求体缓冲

5. **代理处理器** (`internal/proxy/handler.go`)
   - 整合上述组件
   - 错误处理

**验收标准**：
- 配置一个 Claude 供应商后
- `/v1/messages` 普通请求可用
- `/v1/messages` 流式请求可用
- 未知路径透传

---

### Phase 3: 供应商选择与健康管理

**目标**：多供应商自动切换，熔断恢复

**任务清单**：

1. **数据模型存储** (`internal/store/`)
   - Provider CRUD (使用 GORM)
   - Group CRUD
   - HealthState 读写
   - RuntimeConfig 读写

2. **选择策略** (`internal/selector/strategy.go`)
   - 优先级策略
   - 随机策略
   - 权重策略

3. **选择器实现** (`internal/selector/selector.go`)
   - API Type 过滤
   - 组间选择
   - 组内选择

4. **粘性缓存** (`internal/selector/sticky.go`)
   - 内存实现 (sync.Map + TTL)
   - Get/Set/Delete
   - **注意**：内存存储，服务重启后缓存清空（预期行为）

5. **熔断器** (`internal/health/circuit.go`)
   - 滑动窗口计数：使用时间戳列表记录失败时间点
   - 内存实现，服务重启后失败计数清零
   - 自动禁用：窗口内失败次数达阈值时触发
   - 自动恢复：禁用时间到期后自动解除

6. **健康管理器** (`internal/health/manager.go`)
   - MarkSuccess / MarkFailure
   - IsAvailable
   - 手动启用/禁用

7. **并发限制器** (`internal/selector/concurrency.go`)
   - 每供应商使用 `sync.Map + atomic.Int64` 原子计数器记录当前并发数
   - 选择供应商时检查 `当前并发 < concurrency 配置`（concurrency=0 表示不限）
   - 超限行为：**直接跳过该供应商**，选择下一个可用供应商（不等待）
   - 请求开始时 +1，请求结束时（无论成功失败）-1
   - 实现示例：
     ```go
     type ConcurrencyLimiter struct {
         counts sync.Map // map[providerID]*atomic.Int64
     }
     func (l *ConcurrencyLimiter) TryAcquire(providerID string, limit int) bool
     func (l *ConcurrencyLimiter) Release(providerID string)
     ```

8. **重试逻辑** (`internal/proxy/handler.go`)
   - 失败判定 (5xx/429/超时)
   - 写入前可重试
   - 4xx 不重试

**验收标准**：
- 多供应商配置后按策略选择
- 供应商故障自动切换到其他
- 达到阈值后熔断
- 熔断时间到期后恢复
- 并发超限时自动跳过供应商

---

### Phase 4: 管理 API

**目标**：完整的 RESTful 管理接口

**任务清单**：

1. **认证中间件** (`internal/admin/auth.go`)
   - Bearer Token 校验
   - 仅 `/admin/api/*` 需要

2. **供应商 API** (`internal/admin/handler.go`)
   - CRUD
   - enable/disable/reset

3. **分组 API**
   - CRUD

4. **配置 API**
   - 获取全部配置
   - 批量更新配置

5. **状态 API**
   - 系统状态总览
   - 健康状态列表

6. **日志 API**
   - 分页查询
   - 异步写入实现

**验收标准**：
- 所有管理 API 需要认证
- CRUD 操作正常
- 配置修改立即生效

---

### Phase 5: React 管理界面

**目标**：美观实用的管理界面

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

### Phase 6: 完善与发布

**目标**：生产就绪

**任务清单**：

1. **日志清理**
   - 定时任务
   - 按 `log_retention_days` 清理

2. **优雅关闭**
   - 信号监听
   - 等待请求完成
   - 关闭数据库连接

3. **文档**
   - README (部署、配置、使用)
   - 反代配置建议

4. **构建脚本**
   - Windows/Linux/macOS
   - goreleaser 或 Makefile

5. **Docker**
   - Dockerfile
   - docker-compose 示例

**验收标准**：
- 文档完整
- 多平台构建成功
- Docker 可用

---

## 十一、风险点与应对

| 风险 | 影响 | 应对策略 |
|------|------|----------|
| SSE 中途失败无法重试 | 用户体验 | 文档说明；前端可提示重试 |
| 请求体过大内存压力 | 稳定性 | 设置 `max_body_size` 限制 |
| X-Forwarded-For 伪造 | 粘性失效 | 配置 `trust_proxy_headers`；生产部署在可信反代后 |
| API Key 明文存储 | 安全性 | 首期文件权限控制；后续可加密 |
| SQLite 并发写入 | 性能 | GORM + WAL 模式；日志异步批量写入 |

---

## 十二、部署说明


### 构建

```bash
# 构建前端
cd web && npm install && npm run build && cd ..

# 构建后端 (嵌入前端)
go build -o switch-a ./cmd/switch-a
```

### 运行

```bash
SWITCHA_PORT=8080 \
SWITCHA_DB_PATH=./data.db \
SWITCHA_ADMIN_TOKEN=your-secure-token \
./switch-a
```