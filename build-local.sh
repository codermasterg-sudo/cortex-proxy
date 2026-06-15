#!/bin/bash

# Cortex Proxy 本地编译脚本（需要 Go 1.22+）
# 如果有网络问题用 Docker 方案：docker build -f Dockerfile.build -t cortex-proxy:dev .

set -e

echo "=========================================="
echo "🔨 Cortex Proxy 本地编译"
echo "=========================================="
echo ""

# 检查 Go
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装，请先安装 Go 1.22+"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}')
echo "✓ Go 版本：$GO_VERSION"
echo ""

VERSION="${VERSION:-dev}"

# 下载依赖
echo "📦 下载依赖..."
go mod download || {
    echo "⚠️ 依赖下载失败，尝试使用本地缓存..."
}

echo ""
echo "🔨 编译..."

# 编译本地二进制
echo "  1. 本地（$GOOS/$GOARCH）..."
go build -ldflags="-X main.version=$VERSION" -o cortex-proxy .
echo "     ✓ ./cortex-proxy"

# 可选的多平台编译
if [ "$BUILD_ALL" = "1" ]; then
    echo ""
    echo "  编译多平台二进制..."
    mkdir -p dist

    echo "  2. Linux x86_64..."
    GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o dist/cortex-proxy-linux-amd64 .
    echo "     ✓ dist/cortex-proxy-linux-amd64"

    echo "  3. Linux ARM64..."
    GOOS=linux GOARCH=arm64 go build -ldflags="-X main.version=$VERSION" -o dist/cortex-proxy-linux-arm64 .
    echo "     ✓ dist/cortex-proxy-linux-arm64"

    echo "  4. macOS x86_64..."
    GOOS=darwin GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o dist/cortex-proxy-darwin-amd64 .
    echo "     ✓ dist/cortex-proxy-darwin-amd64"

    echo "  5. macOS ARM64..."
    GOOS=darwin GOARCH=arm64 go build -ldflags="-X main.version=$VERSION" -o dist/cortex-proxy-darwin-arm64 .
    echo "     ✓ dist/cortex-proxy-darwin-arm64"

    echo "  6. Windows x86_64..."
    GOOS=windows GOARCH=amd64 go build -ldflags="-X main.version=$VERSION" -o dist/cortex-proxy-windows-amd64.exe .
    echo "     ✓ dist/cortex-proxy-windows-amd64.exe"
fi

echo ""
echo "✅ 编译完成！"
echo ""
echo "📝 使用方法："
echo "  1. 安装证书："
echo "     ./cortex-proxy install"
echo ""
echo "  2. 启动代理："
echo "     ./cortex-proxy start --api-key=<YOUR_API_KEY>"
echo ""
echo "  3. 设置环境变量："
echo "     export HTTPS_PROXY=http://localhost:7898"
