# LanShare 一键安装脚本（Windows）
# 用法（任选其一，在 PowerShell 中执行）:
#   irm https://raw.githubusercontent.com/BruceZlx/lanshare/main/install.ps1 | iex
#
# 做了什么:
#   1. 从 GitHub Release 下载最新 Windows 版
#   2. 安装到 %LOCALAPPDATA%\LanShare（无需管理员权限）
#   3. 自动写入用户 PATH（注册表，对新开的终端生效）
#   4. 当前会话立即可用
$ErrorActionPreference = "Stop"

# 老版本 PowerShell 启用 TLS 1.2
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch {}
$ProgressPreference = "SilentlyContinue"   # 大幅加快 Invoke-WebRequest 下载

$Repo = "BruceZlx/lanshare"

function Say($msg) { Write-Host "===> $msg" -ForegroundColor Green }

Say "获取最新版本..."
$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
$ver = $rel.tag_name
$asset = $rel.assets | Where-Object { $_.name -like "*windows-amd64.zip" } | Select-Object -First 1
if (-not $asset) { throw "没有找到 Windows 版本的下载文件（$ver）" }

$tmp = Join-Path $env:TEMP "lanshare-install-$ver"
if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
New-Item -ItemType Directory -Path $tmp | Out-Null

$zip = Join-Path $tmp $asset.name
Say "下载 $($asset.name) ..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zip

Say "解压并安装..."
Expand-Archive -Path $zip -DestinationPath $tmp -Force

$installDir = Join-Path $env:LOCALAPPDATA "LanShare"
New-Item -ItemType Directory -Path $installDir -Force | Out-Null
$exe = Join-Path $installDir "lanshare.exe"
if (Test-Path $exe) {
    try { Remove-Item $exe -Force -ErrorAction Stop }
    catch { throw "无法覆盖 $exe（lanshare 可能正在运行），请先关闭后重试" }
}
Move-Item -Force (Join-Path $tmp "lanshare.exe") $exe

# 写入用户 PATH（新开终端生效）
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -split ";" -notcontains $installDir) {
    [Environment]::SetEnvironmentVariable("Path", "$installDir;$userPath", "User")
    Say "已加入用户 PATH: $installDir"
} else {
    Say "PATH 已包含 $installDir"
}

# 当前会话立即可用
if ($env:Path -split ";" -notcontains $installDir) { $env:Path = "$installDir;$env:Path" }

Say "安装完成: $(& $exe -version)"
Write-Host ""
Write-Host "现在可以直接使用（建议新开一个终端窗口）:" -ForegroundColor Yellow
Write-Host "  lanshare          启动（交互选择 Web / 终端模式）"
Write-Host "  lanshare term     直接进入终端模式"
Write-Host "  lanshare web      直接进入 Web 模式"
Write-Host "  lanshare send -to <设备名|IP[:端口]> 文件...   一次性发送"
Write-Host ""
Write-Host "卸载: 删除 $installDir 并从用户 PATH 中移除该目录。"
