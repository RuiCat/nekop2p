#!/bin/bash
# Proto 代码生成脚本 (v5.0)
#
# 用法:
#   ./generate.sh             生成所有 proto
#   ./generate.sh local       仅生成本地 API
#   ./generate.sh chains      仅生成双链模块 (需要 cosmos proto 依赖)
#
# 依赖:
#   protoc >= 3.15
#   protoc-gen-go (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest)
#   protoc-gen-go-grpc (go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest)
#
# Cosmos 依赖 (chains 模式):
#   buf (https://buf.build/docs/installation)
#   或手动安装 cosmos-sdk proto

set -e
cd "$(dirname "$0")"

# 确保 protoc 插件在 PATH 中
export PATH="$PATH:$(go env GOPATH)/bin"

MODE="${1:-all}"

generate_local() {
    echo "=== 生成本地 API (local/service.proto) ==="
    protoc \
        --go_out=.. \
        --go_opt=paths=source_relative \
        --go-grpc_out=.. \
        --go-grpc_opt=paths=source_relative \
        nekop2p/local/service.proto 2>/dev/null && {
        echo "✅ local/service.proto → localapi/"
        ls -la ../localapi/service*.pb.go 2>/dev/null || echo "   (生成到 ../ 根目录)"
    } || {
        echo "⚠️  生成失败: 可能需要安装 protoc-gen-go-grpc"
        echo "   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
    }
}

generate_simple() {
    echo "=== 生成 nekop2p 核心类型 ==="
    protoc \
        --go_out=.. \
        --go_opt=paths=source_relative \
        nekop2p.proto 2>/dev/null && {
        echo "✅ nekop2p.proto → nekop2p.pb.go"
    } || {
        echo "⚠️  nekop2p.proto 生成失败 (可忽略)"
    }
}

generate_chains() {
    echo "=== 生成双链模块 (需要 cosmos proto 依赖) ==="
    echo ""
    echo "此步骤需要 cosmos-sdk proto 文件。推荐使用 buf:"
    echo ""
    echo "  1. 安装 buf: https://buf.build/docs/installation"
    echo "  2. 创建 buf.gen.yaml (已提供)"
    echo "  3. buf generate"
    echo ""
    echo "或手动指定 proto 路径:"
    echo "  protoc -I. -I\$(go env GOMODCACHE)/github.com/cosmos/cosmos-sdk@v0.54.3/proto \\"
    echo "    --go_out=.. --go_opt=paths=source_relative \\"
    echo "    nekop2p/brightchain/types.proto nekop2p/brightchain/tx.proto"
}

case "$MODE" in
    all)
        generate_simple
        generate_local
        ;;
    local)
        generate_local
        ;;
    chains)
        generate_chains
        ;;
    *)
        echo "用法: $0 {all|local|chains}"
        ;;
esac

echo ""
echo "💡 提示: 当前使用手写 Go 类型 (types_v2.go)。"
echo "   Proto 生成是可选的优化步骤 (Phase 6)。"
