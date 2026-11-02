#!/bin/sh
# 构建插件到 dist/plugins/<NAME>.wasm，并生成 dist/plugins/version.json（版本、构建时间、commit）。
# 版本取自插件 main.go 的 pluginVersion；commit 与构建时间注入 manifest。
# 用法：bash build-plugins.sh
set -eu

NAME=${NAME:-spotify}
SRC=${SRC:-.}
OUT_DIR=${OUT_DIR:-dist/plugins}

cd "$(dirname "$0")"
VERSION=$(sed -n 's/^const pluginVersion = "\(.*\)"$/\1/p' "$SRC/main.go")
COMMIT_SHA=$(git rev-parse HEAD 2>/dev/null || true)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p "$OUT_DIR"
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath \
    -ldflags="-s -w -X main.commitSHA=$COMMIT_SHA -X main.buildTime=$BUILD_TIME" \
    -o "$OUT_DIR/$NAME.wasm" "$SRC"
printf '{"version": "%s", "build_time": "%s", "commit": "%s"}\n' \
    "$VERSION" "$BUILD_TIME" "$COMMIT_SHA" > "$OUT_DIR/version.json"
echo "$OUT_DIR/$NAME.wasm $VERSION $COMMIT_SHA $BUILD_TIME"
