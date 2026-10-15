#!/usr/bin/env bash
# 本地构建所有平台并打包到 dist/（发布 Release 用）
# 用法: scripts/build-release.sh <版本号>   例如: scripts/build-release.sh v1.2.0
set -euo pipefail

VER="${1:?用法: scripts/build-release.sh <版本号，如 v1.2.0>}"
cd "$(dirname "$0")/.."
OUT="${2:-dist}"

PLATFORMS=(
  "darwin/arm64"
  "darwin/amd64"
  "windows/amd64"
  "linux/amd64"
  "linux/arm64"
)

rm -rf "$OUT"
mkdir -p "$OUT"

for p in "${PLATFORMS[@]}"; do
  GOOS="${p%/*}"
  GOARCH="${p#*/}"
  NAME="lanshare-$VER-$GOOS-$GOARCH"
  mkdir -p "$OUT/$NAME"
  if [ "$GOOS" = "windows" ]; then
    BIN="lanshare.exe"
  else
    BIN="lanshare"
  fi
  echo "==> 构建 $NAME"
  GOOS=$GOOS GOARCH=$GOARCH go build -trimpath -ldflags "-s -w -X main.version=$VER" -o "$OUT/$NAME/$BIN" .
  if [ "$GOOS" = "windows" ]; then
    (cd "$OUT/$NAME" && zip -q "../$NAME.zip" "$BIN")
  else
    (cd "$OUT/$NAME" && tar czf "../$NAME.tar.gz" "$BIN")
  fi
  rm -rf "$OUT/$NAME"
done

echo "==> 完成:"
ls -lh "$OUT"
