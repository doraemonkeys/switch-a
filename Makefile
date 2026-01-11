.PHONY: ci verify lint coverage sloc clean test fmt build release-windows release-clean web-lint web-coverage

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
	@set -o pipefail && go test ./... -coverprofile=./cover.out -covermode=atomic 2>&1 | tail -n 10
	@${GOBIN}/go-test-coverage --config=./.testcoverage.yml
	@golangci-lint run
	@sloc-guard -q check
	@cd web && pnpm lint --quiet
	@cd web && pnpm test:coverage --silent

# 正常模式
verify: coverage lint sloc fmt web-lint web-coverage

lint:
	golangci-lint run

coverage:
	mkdir -p .tmp
	go test ./... -coverprofile=./cover.out -covermode=atomic
	${GOBIN}/go-test-coverage --config=./.testcoverage.yml

sloc:
	mkdir -p .tmp
	sloc-guard check

test:
	go test -v -race ./...

fmt:
	go fmt ./...

# 生成覆盖率报告并在终端显示
cover:
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	go tool cover -func=./cover.out

# 生成覆盖率报告并在浏览器中打开 HTML 可视化页面
cover-html:
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	go tool cover -html=./cover.out

clean:
	rm -rf .tmp cover.out coverage.out

build:
	go build

release-windows:
	bash release.sh windows

release-clean:
	bash release.sh clean

# Web 前端检查
web-lint:
	cd web && pnpm lint

web-coverage:
	cd web && pnpm test:coverage
