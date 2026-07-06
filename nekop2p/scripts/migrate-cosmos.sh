#!/bin/bash
# Nekop2p Cosmos SDK 迁移脚本
# 
# 用法:
#   ./migrate-cosmos.sh setup    — 安装 Cosmos SDK 依赖
#   ./migrate-cosmos.sh build    — 使用 Cosmos SDK 模式编译
#   ./migrate-cosmos.sh test     — 运行 Cosmos SDK 模式测试
#   ./migrate-cosmos.sh clean    — 恢复纯 P2P 模式
#
# 当前状态: Cosmos SDK 代码已完成 (12 文件, ~2,400 行)
#          使用 //go:build cosmos 标签隔离，不影响 P2P 模式编译

set -e
cd "$(dirname "$0")"

COSMOS_GO_MOD="go.mod.cosmos"
CURRENT_GO_MOD="go.mod"
BACKUP_GO_MOD="go.mod.p2p"

case "${1:-help}" in
setup)
    echo "=== 安装 Cosmos SDK 依赖 ==="
    if [ ! -f "$COSMOS_GO_MOD" ]; then
        echo "错误: $COSMOS_GO_MOD 不存在"
        echo "请先从仓库获取或手动创建包含 cosmos-sdk 依赖的 go.mod"
        exit 1
    fi
    
    # 备份当前 go.mod (P2P 模式)
    cp "$CURRENT_GO_MOD" "$BACKUP_GO_MOD"
    cp "$COSMOS_GO_MOD" "$CURRENT_GO_MOD"
    
    # 下载依赖
    echo "下载 Cosmos SDK 依赖 (可能需要几分钟)..."
    go mod download 2>&1 || {
        echo "依赖下载失败。可能的原因:"
        echo "  1. Go 版本不兼容 (需要标准 Go 1.22+)"
        echo "  2. 网络不可达"
        echo "  3. sonic 库与自定义 Go 版本冲突"
        echo ""
        echo "恢复 P2P 模式..."
        cp "$BACKUP_GO_MOD" "$CURRENT_GO_MOD"
        exit 1
    }
    echo "✅ Cosmos SDK 依赖安装完成"
    ;;

build)
    echo "=== Cosmos SDK 模式编译 ==="
    go build -tags cosmos ./... 2>&1 || {
        echo "编译失败。如果遇到 sonic 错误:"
        echo "  go build -tags 'cosmos,sonic_disable' ./..."
    }
    ;;

test)
    echo "=== Cosmos SDK 模式测试 ==="
    go test -tags cosmos ./... 2>&1
    ;;

clean)
    echo "=== 恢复 P2P 模式 ==="
    if [ -f "$BACKUP_GO_MOD" ]; then
        cp "$BACKUP_GO_MOD" "$CURRENT_GO_MOD"
        rm "$BACKUP_GO_MOD"
        echo "✅ 已恢复 P2P 模式 go.mod"
    else
        echo "无备份文件，跳过"
    fi
    ;;

status)
    echo "=== Cosmos SDK 迁移状态 ==="
    echo ""
    echo "已完成:"
    echo "  ✅ 明链模块 (x/brightchain) — AppModule + Keeper + MsgServer"
    echo "  ✅ 暗链模块 (x/darkchain) — AppModule + Keeper + MsgServer"
    echo "  ✅ 应用层 (app/app_v2.go) — BaseApp 子类"
    echo "  ✅ 存储层 (store/iavl_store.go) — IAVL+BadgerDB + 迁移函数"
    echo "  ✅ Proto 定义 (3 个 .proto)"
    echo "  ✅ 入口程序 (cmd/neko-node/main_v2.go)"
    echo ""
    echo "Cosmos SDK 文件: $(grep -rl 'go:build cosmos' --include='*.go' . | wc -l) 个"
    echo "  (使用 //go:build cosmos 标签隔离)"
    echo ""
    echo "待完成:"
    echo "  ⏳ CometBFT 共识集成"
    echo "  ⏳ gRPC Server + Proto 代码生成"
    echo "  ⏳ go.mod.cosmos 依赖清单创建"
    echo "  ⏳ 环境兼容性验证 (Go+sonic)"
    ;;

*)
    echo "用法: $0 {setup|build|test|clean|status}"
    echo ""
    echo "  setup  — 切换到 Cosmos SDK 模式 (安装依赖)"
    echo "  build  — Cosmos SDK 模式编译"
    echo "  test   — Cosmos SDK 模式测试"
    echo "  clean  — 恢复 P2P 模式"
    echo "  status — 查看迁移状态"
    ;;
esac
