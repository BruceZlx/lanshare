#!/usr/bin/env bash
# LanShare 一键安装脚本（macOS / Linux）
# 用法: curl -fsSL https://raw.githubusercontent.com/BruceZlx/lanshare/main/install.sh | bash
#
# 做了什么:
#   1. 自动识别系统与 CPU 架构，从 GitHub Release 下载最新版
#   2. 安装到 ~/.local/bin（无需 sudo）
#   3. 自动配置 shell 的 PATH（.zshrc / .bashrc / .profile）
#   4. macOS 上自动移除 quarantine，避免 Gatekeeper 拦截
set -euo pipefail

REPO="BruceZlx/lanshare"

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m错误:\033[0m %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "需要 curl，请先安装（macOS 自带）"
command -v tar >/dev/null 2>&1 || die "需要 tar，请先安装"

# ---------- 平台识别 ----------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
MACH="$(uname -m)"
case "$OS-$MACH" in
  darwin-arm64 | darwin-aarch64) PLATFORM="darwin-arm64" ;;
  darwin-x86_64 | darwin-amd64) PLATFORM="darwin-amd64" ;;
  linux-x86_64 | linux-amd64) PLATFORM="linux-amd64" ;;
  linux-aarch64 | linux-arm64) PLATFORM="linux-arm64" ;;
  *) die "不支持的系统架构: $OS-$MACH（本脚本支持 macOS / Linux）" ;;
esac
say "检测到平台: $PLATFORM"

# ---------- 解析最新版本（跟随 releases/latest 跳转，不依赖 API 限额） ----------
VER="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's|.*/tag/||')"
case "$VER" in
  v*.*) ;;
  *) die "解析最新版本号失败: ${VER:-空}" ;;
esac

ASSET="lanshare-$VER-$PLATFORM.tar.gz"
URL="https://github.com/$REPO/releases/download/$VER/$ASSET"
say "下载 $URL"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fSL --progress-bar "$URL" -o "$TMP/$ASSET"
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/lanshare" ] || die "压缩包里没有找到 lanshare 可执行文件"

# ---------- 安装 ----------
BIN_DIR="${LANSHARE_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$BIN_DIR"
mv -f "$TMP/lanshare" "$BIN_DIR/lanshare"
chmod +x "$BIN_DIR/lanshare"

# macOS: 移除下载隔离属性，避免 Gatekeeper 拦截
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "$BIN_DIR/lanshare" 2>/dev/null || true
fi

# ---------- 配置 PATH ----------
if [ -z "${LANSHARE_NO_PATH:-}" ]; then
  case ":$PATH:" in
    *":$BIN_DIR:"*) say "PATH 已包含 $BIN_DIR" ;;
    *)
      LINE="export PATH=\"$BIN_DIR:\$PATH\""
      case "$(basename "${SHELL:-bash}")" in
        zsh) RC="$HOME/.zshrc" ;;
        bash)
          if [ "$OS" = "darwin" ]; then RC="$HOME/.bash_profile"; else RC="$HOME/.bashrc"; fi
          ;;
        *) RC="" ;;
      esac
      if [ -n "$RC" ]; then
        touch "$RC"
        if ! grep -qsF "$LINE" "$RC"; then
          printf '\n# LanShare (added by installer)\n%s\n' "$LINE" >> "$RC"
          say "已写入 $RC"
        fi
      fi
      if ! grep -qsF "$LINE" "$HOME/.profile" 2>/dev/null; then
        printf '\n# LanShare (added by installer)\n%s\n' "$LINE" >> "$HOME/.profile" 2>/dev/null || true
      fi
      export PATH="$BIN_DIR:$PATH"
      ;;
  esac
fi

# ---------- 验证 ----------
V="$("$BIN_DIR/lanshare" -version 2>/dev/null || echo 未知)"
say "安装完成: $BIN_DIR/lanshare ($V)"
cat <<'EOF'

现在可以直接使用（若提示命令不存在，请新开一个终端窗口）:

  lanshare          启动（交互选择 Web / 终端模式）
  lanshare term     直接进入终端模式
  lanshare web      直接进入 Web 模式
  lanshare send -to <设备名|IP[:端口]> 文件...   一次性发送

卸载: 删除 ~/.local/bin/lanshare 并移除 shell 配置里的对应 PATH 行。
EOF
