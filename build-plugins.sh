#!/bin/sh
# 构建插件并 gzip 压缩到 dist/plugins/<NAME>.wasm.gz（bot 直接加载 .wasm.gz），
# 同时生成 dist/plugins/version.json（版本、构建时间、commit、压缩文件的 sha256）。
# 版本取自插件 main.go 的 pluginVersion；commit 与构建时间注入 manifest。
# 设置 RELEASE_URL 时把 $RELEASE_URL/<NAME>.wasm.gz 与 $RELEASE_URL/version.json 作为更新地址注入。
# 用法：bash build-plugins.sh
set -eu

NAME=${NAME:-spotify}
SRC=${SRC:-.}
OUT_DIR=${OUT_DIR:-dist/plugins}

cd "$(dirname "$0")"
VERSION=$(sed -n 's/^const pluginVersion = "\(.*\)"$/\1/p' "$SRC/main.go")
COMMIT_SHA=$(git rev-parse HEAD 2>/dev/null || true)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X main.commitSHA=$COMMIT_SHA -X main.buildTime=$BUILD_TIME"
if [ -n "${RELEASE_URL:-}" ]; then
    LDFLAGS="$LDFLAGS -X main.updateURL=$RELEASE_URL/$NAME.wasm.gz -X main.versionURL=$RELEASE_URL/version.json"
fi

mkdir -p "$OUT_DIR"
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath \
    -ldflags="$LDFLAGS" -o "$OUT_DIR/$NAME.wasm" "$SRC"
gzip -9 -n -f "$OUT_DIR/$NAME.wasm"
SHA256=$( (sha256sum "$OUT_DIR/$NAME.wasm.gz" 2>/dev/null || shasum -a 256 "$OUT_DIR/$NAME.wasm.gz") | cut -d' ' -f1)
printf '{"version": "%s", "build_time": "%s", "commit": "%s", "sha256": "%s"}\n' \
    "$VERSION" "$BUILD_TIME" "$COMMIT_SHA" "$SHA256" > "$OUT_DIR/version.json"
echo "$OUT_DIR/$NAME.wasm.gz $VERSION $COMMIT_SHA $BUILD_TIME"
