# Switch-A

English | [简体中文](README-ZH.md)

**A self-hosted AI API gateway for reliable access to multiple providers.**

Switch-A gives your applications one stable endpoint. It selects an eligible provider, forwards the request in its native API format, retries or fails over when appropriate, and records each attempt for troubleshooting.

It includes a browser-based admin UI, stores its data in SQLite, and ships the frontend inside the Go binary.

## Why Switch-A over a typical "X-to-API" gateway?

- **Transparent forwarding instead of brittle conversion.** Except where routing, authentication, or protocol safety requires it, Switch-A leaves request bodies untouched and does not hard-code upstream-specific headers or parameters. That should be a basic gateway capability, yet many "X-to-API" tools get it wrong. New model names and request fields pass through automatically, so upstream model releases do not require a Switch-A update. You also avoid the shared content fingerprints common to hard-coded GPT proxies, reducing the risk of detection, mass flagging, or account bans.
- **The strongest GPT WebSocket adaptation and failover available.** WebSocket handling is a first-class part of Switch-A, not a compatibility patch. Connection setup, streaming lifecycle, safe provider switching, session continuity, and per-attempt diagnostics are modeled end to end.

## Features

- Route traffic by priority, weight, or random selection.
- Keep requests available with retries, circuit breaking, and automatic failover.
- Constrain routing by vendor, group, API type, or model.
- Preserve provider affinity with sticky sessions.
- Protect providers with per-provider concurrency limits.
- Inspect live requests, latency, token usage, health, and every retry attempt.
- Manage providers, groups, routing policies, and runtime settings from a web UI.

## Supported API contracts

Use an explicit namespace in the client base URL when different providers use similar paths:

| Contract | Recommended base URL |
| --- | --- |
| Claude Messages | `http://localhost:28080/claude` |
| Codex / OpenAI Responses | `http://localhost:28080/codex` |
| Gemini native API | `http://localhost:28080/gemini` |
| Grok Chat Completions | `http://localhost:28080/grok` |
| Custom upstream | `http://localhost:28080/custom/{tool-id}` |

The namespace is removed before forwarding. Switch-A preserves the upstream API contract; it does not translate one provider's request format into another.

## Quick start

Requirements:

- Go 1.25 or newer
- Node.js 24 and pnpm 11

Prebuilt Linux, macOS, and Windows archives are available from [GitHub Releases](https://github.com/doraemonkeys/switch-a/releases). Each release includes SHA-256 checksums and GitHub build provenance. Run `switch-a --version` to inspect a binary's release identity.

After cloning the repository, create a local configuration file:

```sh
cp config.example.yaml config.yaml
```

On PowerShell, use `Copy-Item config.example.yaml config.yaml` instead. Open `config.yaml` and replace `your-secret-token-here` with a long, private admin token.

Build the admin UI and start the gateway:

```sh
cd web
pnpm install
pnpm build
cd ..
go run ./cmd/switch-a
```

Then:

1. Open `http://localhost:28081/admin/`.
2. Sign in with the admin token from `config.yaml`.
3. Add at least one provider and configure its supported API type and base URL.
4. Point your client to the matching gateway base URL from the table above.

The default proxy address is `http://localhost:28080`. Runtime data is stored in `data.db` by default.

To build a reusable binary after the frontend has been built:

```sh
go build -o switch-a ./cmd/switch-a
```

On Windows, use `go build -o switch-a.exe ./cmd/switch-a` so the output has the expected executable extension.

## Configuration

Startup settings can come from environment variables or `config.yaml`. Environment variables take precedence.

| Environment variable | Purpose | Default |
| --- | --- | --- |
| `SWITCHA_ADMIN_TOKEN` | Protects the admin API; required | none |
| `SWITCHA_PORT` | Proxy port | `28080` |
| `SWITCHA_ADMIN_PORT` | Admin UI and API port | `28081` |
| `SWITCHA_DB_PATH` | SQLite database path | `./data.db` |
| `SWITCHA_LOG_PATH` | Log file path | `./logs/switch-a.log` |
| `SWITCHA_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` | `info` |

Most routing and reliability settings are managed in the admin UI and persisted in SQLite. See [`config.example.yaml`](config.example.yaml) for all startup options.

## Security

> **Do not expose Switch-A directly to the public internet.** Both servers listen on all network interfaces by default. The admin token protects the admin API, but proxy routes currently have no client authentication.

For remote access, place Switch-A behind a private network or an authenticated reverse proxy with TLS. Keep `config.yaml`, the SQLite database, and exported configurations private because they may contain provider credentials.

## Development

Run the backend and frontend tests:

```sh
go test -race ./...
cd web
pnpm test:coverage
```

Project layout:

- `cmd/switch-a/` — application entry point
- `internal/` — routing, proxying, health, persistence, and admin APIs
- `web/` — React admin UI
- [`docs/PROJECT_OVERVIEW.md`](docs/PROJECT_OVERVIEW.md) — concise architecture map

Switch-A is pre-1.0. Configuration and internal APIs may change between releases.

## Contributing

Bug reports and focused pull requests are welcome. Please include tests for behavior changes and make sure backend and frontend checks pass before submitting.

## License

Licensed under the [Apache License 2.0](LICENSE).
