#!/bin/bash
# 打包脚本：将 build/ 下的二进制打包为完整分发包
# 用法: ./scripts/package.sh <build_dir> <binary_name> <version>
set -e

BUILD_DIR="$1"
BINARY_NAME="$2"
VERSION="$3"

if [ -z "$VERSION" ]; then
    echo "Usage: $0 <build_dir> <binary_name> <version>"
    exit 1
fi

cd "$(dirname "$0")/.."

count=0
for f in "$BUILD_DIR"/"$BINARY_NAME"-"$VERSION"-*; do
    [ -f "$f" ] || continue
    # 跳过已有的归档文件
    case "$f" in
        *.gz|*.zip|*.tar.gz) continue ;;
    esac

    platform=$(echo "$f" | sed "s|$BUILD_DIR/$BINARY_NAME-$VERSION-||")
    pkgname="$BINARY_NAME-$VERSION-$platform"
    pkgdir="/tmp/$pkgname"
    rm -rf "$pkgdir"
    mkdir -p "$pkgdir/static"

    # 复制二进制
    case "$platform" in
        win-*) cp "$f" "$pkgdir/$BINARY_NAME.exe" ;;
        *)     cp "$f" "$pkgdir/$BINARY_NAME" && chmod +x "$pkgdir/$BINARY_NAME" ;;
    esac

    # 复制静态文件和配置
    cp static/index.html static/todo.html "$pkgdir/static/"
    cp config.yaml README.md "$pkgdir/"

    # 打包
    if echo "$platform" | grep -q '^win-'; then
        (cd /tmp && zip -r "$pkgname.zip" "$pkgname/" > /dev/null)
        mv "/tmp/$pkgname.zip" "$BUILD_DIR/"
        echo "  ✓ $pkgname.zip"
    else
        tar czf "$BUILD_DIR/$pkgname.tar.gz" -C /tmp "$pkgname/"
        echo "  ✓ $pkgname.tar.gz"
    fi

    rm -rf "$pkgdir"
    count=$((count + 1))
done

echo "Packaged $count binaries."
