#!/bin/bash
# Proto 代码生成脚本
# 需要安装: protoc + protoc-gen-go
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

set -e
cd "$(dirname "$0")"

echo "生成 nekop2p.pb.go..."
protoc --go_out=.. --go_opt=paths=source_relative nekop2p.proto 2>/dev/null && echo "✅ 生成成功" || {
    echo "⚠️ protoc 未安装或生成失败"
    echo "安装方法:"
    echo "  apt install protobuf-compiler"
    echo "  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"
    exit 1
}
