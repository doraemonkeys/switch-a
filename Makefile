.PHONY: ci verify lint coverage sloc clean test fmt build build-all web-build release-windows release-clean web-lint web-coverage web-tsc

SHELL := /bin/bash
.SHELLFLAGS := -o pipefail -c

# 设置 GOBIN 环境变量，默认为 $(go env GOPATH)/bin
GOBIN ?= $$(go env GOPATH)/bin

# 跨平台临时目录设置
ifeq ($(OS),Windows_NT)
    # Windows (Git Bash / MSYS2)
    TMP_DIR := $(shell pwd -W)/.tmp
else
    # Linux / macOS
    TMP_DIR := $(CURDIR)/.tmp
endif

TEMP_ENV := TEMP="$(TMP_DIR)" TMP="$(TMP_DIR)"

# 静默模式
ci:
	@mkdir -p .tmp
	@set -o pipefail && go test -race ./... -coverprofile=./cover.out -covermode=atomic 2>&1 | tail -n 10
	@${GOBIN}/go-test-coverage --config=./.testcoverage.yml
	@golangci-lint run
	@sloc-guard -q check
	@cd web && pnpm lint --quiet
	@cd web && pnpm test:coverage --silent
	@cd web && npx tsc --noEmit -p tsconfig.app.json

# 正常模式
verify: coverage lint sloc fmt web-lint web-coverage web-tsc web-fmt

lint:
	golangci-lint run

coverage:
	mkdir -p .tmp
	go test -race ./... -coverprofile=./cover.out -covermode=atomic
	${GOBIN}/go-test-coverage --config=./.testcoverage.yml

sloc:
	mkdir -p .tmp
	sloc-guard check

test:
	go test -v -race ./...

fmt:
	go fmt ./...

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

release-windows:
	bash release.sh windows

release-clean:
	bash release.sh clean

# Web 前端检查
web-lint:
	cd web && pnpm lint

web-coverage:
	cd web && pnpm test:coverage

web-tsc:
	cd web && npx tsc --noEmit -p tsconfig.app.json
