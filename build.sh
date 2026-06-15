#!/bin/bash

# Cortex Proxy 构建脚本
# 使用 Docker 容器进行多平台编译，无需本地 Go 环境

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

VERSION="${VERSION:-dev}"
DOCKER_IMAGE="cortex-proxy-builder:latest"

print_header() {
    echo ""
    echo "=========================================="
    echo "$1"
    echo "=========================================="
    echo ""
}

# 1. 构建 Builder 镜像
print_header "📦 Step 1: 构建编译器镜像"

docker build -f Dockerfile.build \
    --target builder \
    -t "$DOCKER_IMAGE" \
    -q .

echo "✓ Builder 镜像已构建"

# 2. 运行编译器
print_header "🔨 Step 2: 编译所有平台"

docker run --rm \
    -v "$SCRIPT_DIR:/app" \
    -e VERSION="$VERSION" \
    -e CGO_ENABLED=0 \
    "$DOCKER_IMAGE" \
    sh -c "
        set -e

        echo '📝 编译信息：'
        echo '  Version: $VERSION'
        go version
        echo ''

        # 本地二进制（用于运行 docker build -f Dockerfile.build）
        echo '🔨 1. 本地二进制...'
        go build -ldflags='-X main.version=$VERSION' -o cortex-proxy .
        echo '   ✓ ./cortex-proxy'

        # 多平台
        mkdir -p dist

        echo '🔨 2. Linux x86_64...'
        GOOS=linux GOARCH=amd64 go build \
            -ldflags='-X main.version=$VERSION' \
            -o dist/cortex-proxy-linux-amd64 .
        echo '   ✓ dist/cortex-proxy-linux-amd64'

        echo '🔨 3. Linux ARM64...'
        GOOS=linux GOARCH=arm64 go build \
            -ldflags='-X main.version=$VERSION' \
            -o dist/cortex-proxy-linux-arm64 .
        echo '   ✓ dist/cortex-proxy-linux-arm64'

        echo '🔨 4. macOS Intel...'
        GOOS=darwin GOARCH=amd64 go build \
            -ldflags='-X main.version=$VERSION' \
            -o dist/cortex-proxy-darwin-amd64 .
        echo '   ✓ dist/cortex-proxy-darwin-amd64'

        echo '🔨 5. macOS ARM64...'
        GOOS=darwin GOARCH=arm64 go build \
            -ldflags='-X main.version=$VERSION' \
            -o dist/cortex-proxy-darwin-arm64 .
        echo '   ✓ dist/cortex-proxy-darwin-arm64'

        echo '🔨 6. Windows x86_64...'
        GOOS=windows GOARCH=amd64 go build \
            -ldflags='-X main.version=$VERSION' \
            -o dist/cortex-proxy-windows-amd64.exe .
        echo '   ✓ dist/cortex-proxy-windows-amd64.exe'

        echo ''
        echo '✅ 编译完成！'
    "

# 3. 验证输出
print_header "✅ Step 3: 验证输出"

echo "本地二进制："
ls -lh cortex-proxy 2>/dev/null && echo "  ✓ cortex-proxy" || echo "  ✗ cortex-proxy (缺失)"

echo ""
echo "多平台二进制："
ls -lh dist/ 2>/dev/null | tail -n +2 | sed 's/^/  /'

# 4. 显示使用方法
print_header "🚀 使用方法"

if [ -f "./cortex-proxy" ]; then
    echo "1. 安装 CA 证书（首次使用）"
    echo "   ./cortex-proxy install"
    echo ""
    echo "2. 启动代理"
    echo "   ./cortex-proxy start --api-key=<YOUR_API_KEY>"
    echo ""
    echo "3. 设置代理环境变量"
    echo "   export HTTPS_PROXY=http://localhost:7898"
    echo ""
    echo "完整选项说明："
    echo "   ./cortex-proxy start --help"
else
    echo "❌ 编译失败，未找到二进制文件"
    exit 1
fi

print_header "✨ 编译脚本完成"
