# livestream 构建脚本
#
# 常用目标：
#   make build    开发构建，输出 bin/livestream
#   make release  发布构建（裁剪调试信息、静态链接）
#   make run      构建并运行
#   make clean    清理构建产物
#   make vet / fmt / test

APP_NAME   := livestream
BIN_DIR    := bin
BIN_PATH   := $(BIN_DIR)/$(APP_NAME)

# ---- 构建信息（经 -ldflags 注入 main 包变量）----
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# 沙箱环境默认构建缓存不可写，项目约定统一放 /tmp/gocache
GOCACHE ?= /tmp/gocache

LDFLAGS := -X main.version=$(VERSION) \
           -X main.gitCommit=$(GIT_COMMIT) \
           -X main.buildDate=$(BUILD_DATE)

.PHONY: build release run clean vet fmt test

build:
	@mkdir -p $(BIN_DIR)
	GOCACHE=$(GOCACHE) go build -buildvcs=false -trimpath -o $(BIN_PATH) -ldflags "$(LDFLAGS)" .
	@echo "built: $(BIN_PATH)"

release:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) go build -buildvcs=false -trimpath -o $(BIN_PATH) \
		-ldflags "-s -w $(LDFLAGS)" .
	@echo "built (release): $(BIN_PATH)"

run: build
	$(BIN_PATH) -addr 127.0.0.1:8080

clean:
	rm -rf $(BIN_DIR)

vet:
	GOCACHE=$(GOCACHE) go vet ./...

fmt:
	gofmt -l .

test:
	GOCACHE=$(GOCACHE) go test ./...
