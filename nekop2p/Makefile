# Nekop2p Makefile
#
# 双模式构建:
#   make build        P2P 原生模式
#   make build-cosmos Cosmos SDK 模式
#
# 测试:
#   make test         P2P 模式测试
#   make test-cosmos  Cosmos SDK 模式测试
#   make test-all     双模式全量测试 + race

.PHONY: build build-cosmos test test-cosmos test-all vet lint clean run help

# 默认目标
.DEFAULT_GOAL := help

# Go 参数
GOCMD      := go
BUILD_DIR  := ./cmd/neko-node
COSMOS_TAG := cosmos

## build: 编译 P2P 原生模式
build:
	@echo "🔨 编译 P2P 原生模式..."
	$(GOCMD) build ./...

## build-cosmos: 编译 Cosmos SDK 模式
build-cosmos:
	@echo "🔨 编译 Cosmos SDK 模式..."
	$(GOCMD) build -tags $(COSMOS_TAG) ./...

## build-node: 编译 neko-node (P2P)
build-node:
	@echo "🔨 编译 neko-node (P2P)..."
	$(GOCMD) build -o neko-node $(BUILD_DIR)

## build-node-cosmos: 编译 neko-node (Cosmos)
build-node-cosmos:
	@echo "🔨 编译 neko-node (Cosmos)..."
	$(GOCMD) build -tags $(COSMOS_TAG) -o neko-node-cosmos $(BUILD_DIR)

## test: 运行 P2P 模式测试
test:
	@echo "🧪 P2P 模式测试..."
	$(GOCMD) test -timeout 120s -count=1 ./...

## test-cosmos: 运行 Cosmos SDK 模式测试
test-cosmos:
	@echo "🧪 Cosmos SDK 模式测试..."
	$(GOCMD) test -tags $(COSMOS_TAG) -timeout 120s -count=1 ./...

## test-race: 运行竞态检测 (P2P)
test-race:
	@echo "🔍 P2P 竞态检测..."
	$(GOCMD) test -race -timeout 180s -count=1 ./...

## test-race-cosmos: 运行竞态检测 (Cosmos)
test-race-cosmos:
	@echo "🔍 Cosmos 竞态检测..."
	$(GOCMD) test -tags $(COSMOS_TAG) -race -timeout 180s -count=1 ./...

## test-all: 双模式全量测试 + 竞态检测
test-all: test test-cosmos test-race test-race-cosmos
	@echo "✅ 全部测试通过!"

## vet: 代码检查 (P2P)
vet:
	$(GOCMD) vet ./...

## vet-cosmos: 代码检查 (Cosmos)
vet-cosmos:
	$(GOCMD) vet -tags $(COSMOS_TAG) ./...

## lint: golangci-lint
lint:
	golangci-lint run --timeout=5m ./...

## clean: 清理构建产物
clean:
	rm -f neko-node neko-node-cosmos
	$(GOCMD) clean -cache

## run: 启动 P2P 节点
run: build-node
	./neko-node run --data ./testdata

## run-cosmos: 初始化并启动 Cosmos 节点
run-cosmos: build-node-cosmos
	@mkdir -p ~/.nekop2p
	./neko-node-cosmos init --home ~/.nekop2p
	./neko-node-cosmos start --home ~/.nekop2p

## help: 显示帮助
help:
	@echo "Nekop2p 双模式构建系统"
	@echo ""
	@echo "构建:"
	@echo "  make build              P2P 原生模式"
	@echo "  make build-cosmos       Cosmos SDK 模式"
	@echo "  make build-node         neko-node (P2P)"
	@echo "  make build-node-cosmos  neko-node (Cosmos)"
	@echo ""
	@echo "测试:"
	@echo "  make test               P2P 测试"
	@echo "  make test-cosmos        Cosmos 测试"
	@echo "  make test-race          竞态检测"
	@echo "  make test-all           全量测试"
	@echo ""
	@echo "工具:"
	@echo "  make vet                代码检查"
	@echo "  make lint               Linter"
	@echo "  make clean              清理"
	@echo ""
	@echo "运行:"
	@echo "  make run                P2P 节点"
	@echo "  make run-cosmos         Cosmos 节点"
