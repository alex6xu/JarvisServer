#!/usr/bin/env bash
# JarvisServer 构建脚本（在具备 Go 1.27rc1 的机器上执行，例如你的 EPYC 服务器）
# 产出：build/gateway（linux/amd64 静态二进制）+ web/dist/（前端静态产物）
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> go mod download"
go mod download

echo "==> 交叉编译 linux/amd64 网关二进制"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o build/gateway ./cmd/gateway

echo "==> 构建前端"
cd web
npm ci
npm run build
cd ..

echo "==> 完成"
echo "  二进制: build/gateway"
echo "  前端:   web/dist/"
echo "  配置:   deploy/gateway.prod.yaml"
