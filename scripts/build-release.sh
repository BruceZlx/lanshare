#!/usr/bin/env bash
# 本地构建所有平台并打包到 dist/（发布 Release 用）
# 每个平台的压缩包含三个可执行文件：
#   lanshare       启动时询问 Web / 终端模式
#   lanshare-term  启动即进终端模式（免询问）
#   lanshare-web   启动即进 Web 模式（免询问，自动开浏览器）
# 用法: scripts/build-release.sh <版本号>   例如: scripts/build-release.sh v1.3.1
set -euo pipefail

VER="${1:?用法: scripts/build-release.sh <版本号，如 v1.3.1>}"
cd "$(dirname "$0")/.."
OUT="${2:-dist}"

PLATFORMS=(
  "darwin/arm64"
  "darwin/amd64"
  "windows/amd64"
  "linux/amd64"
  "linux/arm64"
)

VARIANTS=("" "-term" "-web")   # 后缀；空 = 交互询问版

rm -rf "$OUT"
mkdir -p "$OUT"

for p in "${PLATFORMS[@]}"; do
  GOOS="${p%/*}"
  GOARCH="${p#*/}"
  NAME="lanshare-$VER-$GOOS-$GOARCH"
  STAGE="$OUT/$NAME"
  mkdir -p "$STAGE"
  if [ "$GOOS" = "windows" ]; then EXT=".exe"; else EXT=""; fi
  for v in "${VARIANTS[@]}"; do
    MODE=""; [ -n "$v" ] && MODE="${v#-}"
    LDFLAGS="-s -w -X main.version=$VER"
    [ -n "$MODE" ] && LDFLAGS="$LDFLAGS -X main.buildMode=$MODE"
    echo "==> 构建 $NAME/lanshare$v"
    GOOS=$GOOS GOARCH=$GOARCH go build -trimpath -ldflags "$LDFLAGS" -o "$STAGE/lanshare$v$EXT" .
  done
  if [ "$GOOS" = "windows" ]; then
    (cd "$STAGE" && zip -q "../$NAME.zip" lanshare$EXT lanshare-term$EXT lanshare-web$EXT)
  else
    (cd "$STAGE" && tar czf "../$NAME.tar.gz" lanshare lanshare-term lanshare-web)
  fi
  rm -rf "$STAGE"
done

echo "==> 完成:"
ls -lh "$OUT"
