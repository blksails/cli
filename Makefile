# bk —— BlackSails Cloud CLI 构建/安装入口
#
# 常用：
#   make install      构建并安装 bk 到 GOBIN（回退 $(go env GOPATH)/bin）
#   make build        在 ./bin/bk 产出本地二进制
#   make test         跑单元/集成测试
#   make e2e          跑全生命周期 e2e（hermetic，无需外部依赖）
#   make uninstall    从安装目录移除 bk

BINARY  := bk
PKG     := .
BINDIR  := bin

# 安装目录：优先 GOBIN，否则 $(GOPATH)/bin。
GOBIN   := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN   := $(shell go env GOPATH)/bin
endif

# 版本信息（best-effort，从 git 取；无 git 时回退 dev）。
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# 注入路径需与 .goreleaser.yaml 保持一致（cmd 包的 version/commit/date 变量）。
VPKG    := pkg.blksails.net/bk/cmd
# -s -w 去符号表减小体积；-X 注入版本元数据。
LDFLAGS := -ldflags "-s -w -X $(VPKG).version=$(VERSION) -X $(VPKG).commit=$(COMMIT) -X $(VPKG).date=$(DATE)"

.DEFAULT_GOAL := help
.PHONY: help install uninstall build test e2e e2e-real clean snapshot release release-check

help: ## 显示可用目标
	@echo "bk Makefile —— 版本 $(VERSION)"
	@echo "安装目录 (GOBIN): $(GOBIN)"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

install: ## 构建并安装 bk 到 GOBIN
	@echo ">> 安装 $(BINARY) ($(VERSION)) 到 $(GOBIN)"
	@GOBIN="$(GOBIN)" go install $(LDFLAGS) $(PKG)
	@echo ">> 已安装：$(GOBIN)/$(BINARY)"
	@command -v $(BINARY) >/dev/null 2>&1 || echo ">> 提示：请确认 $(GOBIN) 在 PATH 中"

uninstall: ## 从 GOBIN 移除 bk
	@rm -f "$(GOBIN)/$(BINARY)" && echo ">> 已移除 $(GOBIN)/$(BINARY)"

build: ## 在 ./bin/bk 产出本地二进制
	@mkdir -p $(BINDIR)
	@go build $(LDFLAGS) -o $(BINDIR)/$(BINARY) $(PKG)
	@echo ">> 已构建 $(BINDIR)/$(BINARY)"

test: ## 跑单元/集成测试
	@go test ./...

e2e: ## 跑全生命周期 e2e（hermetic）
	@go test ./cmd/ -run TestAppLifecycle_E2E -v

e2e-real: ## 跑真实主机 e2e（需 BK_E2E_DOKKU_HOST 等环境变量，见 e2e/README.md）
	@go test ./cmd/ -run TestAppLifecycle_RealHost_E2E -v

clean: ## 清理本地构建产物
	@rm -rf $(BINDIR) dist && echo ">> 已清理 $(BINDIR) dist"

release-check: ## 校验 .goreleaser.yaml 配置
	@goreleaser check

snapshot: ## 本地多平台快照构建（不发布，产物在 ./dist）
	@goreleaser release --snapshot --clean

release: ## 正式发布（需先打 tag 并设置 GITHUB_TOKEN）
	@goreleaser release --clean
