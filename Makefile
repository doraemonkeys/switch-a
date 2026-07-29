.PHONY: ci ci-go ci-web ci-structure verify lint coverage gopls-check sloc clean test fmt format-check build build-all web-build release-windows release-mac release-clean web-lint web-coverage web-tsc web-format-check check-go-env tools install-tools install-go-tools install-sloc-guard ensure-go-test-coverage ensure-golangci-lint ensure-gopls ensure-sloc-guard

SHELL := /bin/bash
.SHELLFLAGS := -o pipefail -c

# Go reports Windows-style GOBIN/GOPATH even though recipes run under Git Bash.
# Keep the native path for `go install`, then convert the execution path for Bash.
GO_ENV_GOBIN := $(shell go env GOBIN)
GO_ENV_GOPATH := $(shell go env GOPATH)
GO_TOOL_BIN_NATIVE := $(if $(GOBIN),$(GOBIN),$(GO_ENV_GOBIN))
ifeq ($(strip $(GO_TOOL_BIN_NATIVE)),)
    GO_TOOL_BIN_NATIVE := $(GO_ENV_GOPATH)/bin
endif
GO_TOOL_BIN := $(GO_TOOL_BIN_NATIVE)
ifeq ($(OS),Windows_NT)
    GO_TOOL_BIN := $(shell cygpath -u "$(GO_TOOL_BIN)")
endif
GO_EXE := $(shell go env GOEXE)
# gopls v0.23 requires Go 1.26, while the project intentionally supports the Go 1.25 toolchain.
GOPLS_VERSION := v0.22.0
GOPLS_MODULE := golang.org/x/tools/gopls
GOPLS := $(GO_TOOL_BIN)/gopls$(GO_EXE)
GO_TEST_COVERAGE_VERSION := v2.17.1
GO_TEST_COVERAGE_MODULE := github.com/vladopajic/go-test-coverage/v2
GO_TEST_COVERAGE := $(GO_TOOL_BIN)/go-test-coverage$(GO_EXE)
GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT_MODULE := github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOLANGCI_LINT := $(GO_TOOL_BIN)/golangci-lint$(GO_EXE)
SLOC_GUARD_VERSION := 0.4.0
ifeq ($(OS),Windows_NT)
    PROJECT_ROOT_NATIVE := $(shell pwd -W)
else
    PROJECT_ROOT_NATIVE := $(CURDIR)
endif
TOOLS_ROOT_NATIVE := $(PROJECT_ROOT_NATIVE)/.tmp/tools
TOOLS_ROOT := $(TOOLS_ROOT_NATIVE)
ifeq ($(OS),Windows_NT)
    TOOLS_ROOT := $(shell cygpath -u "$(TOOLS_ROOT)")
endif
SLOC_GUARD := $(TOOLS_ROOT)/bin/sloc-guard$(GO_EXE)
GOPLS_CHECK := git ls-files -z '*.go' | xargs -0 "$(GOPLS)" check -severity=hint
GOFMT_CHECK := git ls-files -z '*.go' | xargs -0 gofmt -l
REQUIRED_GO_VERSION := $(shell awk '/^go / {print $$2}' go.mod)

# 跨平台临时目录设置
ifeq ($(OS),Windows_NT)
    # Windows (Git Bash / MSYS2)
    TMP_DIR := $(shell pwd -W)/.tmp
else
    # Linux / macOS
    TMP_DIR := $(CURDIR)/.tmp
endif

TEMP_ENV := TEMP="$(TMP_DIR)" TMP="$(TMP_DIR)"

# Go 环境检查 (仅 Windows Git Bash/MSYS2)
check-go-env:
	@if ! command -v go >/dev/null 2>&1; then \
		echo "Go is required but was not found in PATH"; \
		exit 1; \
	fi
	@go_version="$$(go env GOVERSION | sed 's/^go//')"; \
		required_version="$(REQUIRED_GO_VERSION)"; \
		go_major="$${go_version%%.*}"; \
		go_minor_patch="$${go_version#*.}"; \
		go_minor="$${go_minor_patch%%.*}"; \
		required_major="$${required_version%%.*}"; \
		required_minor_patch="$${required_version#*.}"; \
		required_minor="$${required_minor_patch%%.*}"; \
		if [ "$$go_major" -lt "$$required_major" ] || { [ "$$go_major" -eq "$$required_major" ] && [ "$$go_minor" -lt "$$required_minor" ]; }; then \
			echo "Go $$required_version or newer is required; found $$go_version"; \
			exit 1; \
		fi
	@case "$$(uname -s)" in \
		MINGW*|MSYS*|CYGWIN*) \
			if [ -z "$$(go env GOMODCACHE 2>/dev/null)" ] || [ -z "$$(go env GOPATH 2>/dev/null)" ]; then \
				echo ""; \
				echo "❌ Go environment issue detected in current terminal"; \
				echo ""; \
				echo "💡 Recommended: Run with PowerShell:"; \
				echo "   pwsh -Command \"cd '$$(pwd -W)'; make ci\""; \
				echo ""; \
				exit 1; \
			fi \
		;; \
	esac

# Keep local and hosted CI on the same quality entry points while allowing GitHub jobs to run in parallel.
ci: ci-go ci-web ci-structure

ci-go: check-go-env coverage lint format-check gopls-check

ci-web: web-coverage web-tsc web-lint web-format-check

ci-structure: sloc

# 正常模式
verify: check-go-env coverage lint fmt web-coverage web-tsc web-lint web-fmt rm-tmpclaude sloc gopls-check

rm-tmpclaude:
	@rm -f tmpclaude-*

tools: ensure-go-test-coverage ensure-golangci-lint ensure-gopls ensure-sloc-guard

install-tools: install-go-tools install-sloc-guard

install-go-tools:
	GOBIN="$(GO_TOOL_BIN_NATIVE)" go install $(GO_TEST_COVERAGE_MODULE)@$(GO_TEST_COVERAGE_VERSION)
	GOBIN="$(GO_TOOL_BIN_NATIVE)" go install $(GOLANGCI_LINT_MODULE)@$(GOLANGCI_LINT_VERSION)
	GOBIN="$(GO_TOOL_BIN_NATIVE)" go install $(GOPLS_MODULE)@$(GOPLS_VERSION)

install-sloc-guard:
	cargo install sloc-guard --version $(SLOC_GUARD_VERSION) --root "$(TOOLS_ROOT_NATIVE)" --locked --force

ensure-go-test-coverage:
	@if [ ! -x "$(GO_TEST_COVERAGE)" ]; then \
		echo "Missing required tool: go-test-coverage"; \
		echo "Expected executable: $(GO_TEST_COVERAGE)"; \
		echo "Install manually with:"; \
		echo "  GOBIN=\"$(GO_TOOL_BIN_NATIVE)\" go install $(GO_TEST_COVERAGE_MODULE)@$(GO_TEST_COVERAGE_VERSION)"; \
		exit 1; \
	fi

ensure-golangci-lint:
	@if [ ! -x "$(GOLANGCI_LINT)" ]; then \
		echo "Missing required tool: golangci-lint"; \
		echo "Expected executable: $(GOLANGCI_LINT)"; \
		echo "Install manually with:"; \
		echo "  GOBIN=\"$(GO_TOOL_BIN_NATIVE)\" go install $(GOLANGCI_LINT_MODULE)@$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	fi

ensure-gopls:
	@if [ ! -x "$(GOPLS)" ]; then \
		echo "Missing required tool: gopls"; \
		echo "Expected executable: $(GOPLS)"; \
		echo "Install manually with:"; \
		echo "  GOBIN=\"$(GO_TOOL_BIN_NATIVE)\" go install $(GOPLS_MODULE)@$(GOPLS_VERSION)"; \
		exit 1; \
	fi

ensure-sloc-guard:
	@if [ ! -x "$(SLOC_GUARD)" ]; then \
		echo "Missing required tool: sloc-guard"; \
		echo "Expected executable: $(SLOC_GUARD)"; \
		echo "Install manually with:"; \
		echo "  cargo install sloc-guard --version $(SLOC_GUARD_VERSION) --root \"$(TOOLS_ROOT_NATIVE)\" --locked --force"; \
		exit 1; \
	fi

lint: ensure-golangci-lint
	"$(GOLANGCI_LINT)" run

gopls-check: ensure-gopls
	$(GOPLS_CHECK)

coverage: ensure-go-test-coverage
	mkdir -p .tmp
	go test -race ./... -coverprofile=./cover.out -covermode=atomic
	"$(GO_TEST_COVERAGE)" --config=./.testcoverage.yml

sloc: ensure-sloc-guard
	mkdir -p .tmp
	"$(SLOC_GUARD)" check

test:
	go test -v -race ./...

fmt:
	go fmt ./...

format-check:
	@unformatted="$$($(GOFMT_CHECK))"; \
		if [ -n "$$unformatted" ]; then \
			echo "The following Go files are not formatted:"; \
			printf '%s\n' "$$unformatted"; \
			exit 1; \
		fi

web-fmt:
	cd web && pnpm fmt

# 生成覆盖率报告并在终端显示
cover:
	go test -race ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	go tool cover -func=./cover.out

# 生成覆盖率报告并在浏览器中打开 HTML 可视化页面
cover-html:
	go test -race ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	go tool cover -html=./cover.out

clean:
	rm -rf .tmp cover.out coverage.out

# Build frontend only
web-build:
	cd web && pnpm install && pnpm build

# Build Go binary only (requires frontend to be built first)
build:
	go build -o switch-a ./cmd/switch-a

# Build complete release binary with embedded frontend
build-all: web-build build
	@echo "Build complete: switch-a binary with embedded frontend"

# Cross-compile macOS release binaries (arm64 + amd64) with embedded frontend.
# CGO_ENABLED=0 is safe because the SQLite driver is pure Go (modernc.org/sqlite),
# so no C toolchain or macOS SDK is needed on the build host.
release-mac: web-build
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/switch-a-darwin-arm64 ./cmd/switch-a
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/switch-a-darwin-amd64 ./cmd/switch-a
	@echo "Build complete: dist/switch-a-darwin-{arm64,amd64}"

# Web 前端检查
web-lint:
	cd web && pnpm lint

web-format-check:
	cd web && pnpm exec prettier --check src/

web-coverage:
	cd web && pnpm test:coverage

web-tsc:
	cd web && pnpm exec tsc --noEmit -p tsconfig.app.json
