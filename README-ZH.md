# Switch-A

[English](README.md) | 简体中文

**一个可靠接入多个上游的自托管 AI API 网关。**

Switch-A 为应用提供统一、稳定的访问入口。它会按策略选择可用的上游，以原生 API 格式转发请求，在适当时进行重试或故障切换，并记录每次尝试以便排查问题。

项目自带浏览器管理界面，使用 SQLite 保存数据，前端会直接嵌入 Go 二进制文件。

## 相比常见的“X-to-API”网关，Switch-A 有什么不同？

- **尽可能透明地转发，而不是进行脆弱的协议转换。** 除非路由、认证或协议安全确有需要，Switch-A 不会改写请求体，也没有写死上游专用的 Header 和参数，这本来是基础功能但是很多其他软件却没有做好。因此上游发布新模型时通常不需要等待 Switch-A 更新，新的模型名称与请求字段可以自动透传。你也不必担心 GPT 反代会因请求内容特征而被标记检测，降低日后被整体识别、批量风控或封号的风险。
- **全网最佳的 GPT WebSocket 适配与故障切换。** WebSocket 在 Switch-A 中是一等能力，而不是后期拼接的兼容补丁。从连接建立、流式生命周期、安全切换上游、会话连续性到每次尝试的诊断记录，都进行了端到端建模。

## 功能

- 按优先级、权重或随机策略分配流量。
- 通过重试、熔断和自动故障切换保持请求可用。
- 按供应商、分组、API 类型或模型约束路由范围。
- 使用粘性会话保持上游亲和性。
- 通过单上游并发限制保护上游服务。
- 查看实时请求、延迟、Token 用量、健康状态和每次重试记录。
- 在 Web 管理界面中维护上游、分组、路由策略和运行时配置。

## 支持的 API 协议

当不同上游使用相似路径时，建议在客户端 Base URL 中显式指定命名空间：

| 协议 | 推荐 Base URL |
| --- | --- |
| Claude Messages | `http://localhost:28080/claude` |
| Codex / OpenAI Responses | `http://localhost:28080/codex` |
| Gemini 原生 API | `http://localhost:28080/gemini` |
| Grok Chat Completions | `http://localhost:28080/grok` |
| 自定义上游 | `http://localhost:28080/custom/{tool-id}` |

命名空间只用于网关内部路由，转发到上游前会被移除。Switch-A 保留上游原生 API 协议，不负责在不同供应商的请求格式之间进行转换。

## 快速开始

运行环境：

- Go 1.25 或更高版本
- Node.js 24 和 pnpm 11

可从 [GitHub Releases](https://github.com/doraemonkeys/switch-a/releases) 下载 Linux、macOS 和 Windows 预编译包。每个版本都附带 SHA-256 校验文件和 GitHub 构建来源证明；运行 `switch-a --version` 可查看二进制文件的版本身份。

克隆仓库后，创建本地配置文件：

```sh
cp config.example.yaml config.yaml
```

PowerShell 请改用 `Copy-Item config.example.yaml config.yaml`。打开 `config.yaml`，将 `your-secret-token-here` 替换为足够长且仅自己知道的管理员令牌。

构建管理界面并启动网关：

```sh
cd web
pnpm install
pnpm build
cd ..
go run ./cmd/switch-a
```

启动后：

1. 打开 `http://localhost:28081/admin/`。
2. 使用 `config.yaml` 中的管理员令牌登录。
3. 添加至少一个上游，并配置它支持的 API 类型和 Base URL。
4. 将客户端指向上表中对应的网关 Base URL。

默认代理地址为 `http://localhost:28080`，运行数据默认保存在 `data.db`。

前端构建完成后，可以生成可重复使用的二进制文件：

```sh
go build -o switch-a ./cmd/switch-a
```

Windows 请使用 `go build -o switch-a.exe ./cmd/switch-a`，确保生成的文件带有可执行扩展名。

## 配置

启动配置可以来自环境变量或 `config.yaml`，环境变量优先级更高。

| 环境变量 | 用途 | 默认值 |
| --- | --- | --- |
| `SWITCHA_ADMIN_TOKEN` | 保护管理 API；必填 | 无 |
| `SWITCHA_PORT` | 代理端口 | `28080` |
| `SWITCHA_ADMIN_PORT` | 管理界面和管理 API 端口 | `28081` |
| `SWITCHA_DB_PATH` | SQLite 数据库路径 | `./data.db` |
| `SWITCHA_LOG_PATH` | 日志文件路径 | `./logs/switch-a.log` |
| `SWITCHA_LOG_LEVEL` | `debug`、`info`、`warn` 或 `error` | `info` |

大部分路由和可靠性配置都在管理界面中维护，并持久化到 SQLite。所有启动选项见 [`config.example.yaml`](config.example.yaml)。

## 安全说明

> **不要将 Switch-A 直接暴露在公网。** 两个服务默认都会监听所有网络接口。管理员令牌会保护管理 API，但代理路由目前没有客户端鉴权。

如需远程访问，请将 Switch-A 放在私有网络中，或置于启用了身份认证与 TLS 的反向代理之后。`config.yaml`、SQLite 数据库和导出的配置可能包含上游凭据，请妥善保管。

## 开发

运行后端和前端测试：

```sh
go test -race ./...
cd web
pnpm test:coverage
```

项目结构：

- `cmd/switch-a/` — 应用入口
- `internal/` — 路由、代理、健康管理、持久化和管理 API
- `web/` — React 管理界面
- [`docs/PROJECT_OVERVIEW.md`](docs/PROJECT_OVERVIEW.md) — 简洁的架构索引

Switch-A 目前处于 1.0 之前的开发阶段，配置和内部 API 可能会在不同版本间发生变化。

## 参与贡献

欢迎提交问题报告和范围清晰的 Pull Request。修改行为时请补充相应测试，并在提交前确认后端与前端检查均可通过。

## 开源协议

本项目采用 [Apache License 2.0](LICENSE) 开源协议。
