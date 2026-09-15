# LanShare · 局域网互传

[![Release](https://img.shields.io/github/v/release/zzzhhhhhgame-cmd/lanshare?label=%E4%B8%8B%E8%BD%BD&color=4f8cff)](https://github.com/zzzhhhhhgame-cmd/lanshare/releases/latest)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-8b5cf6)](https://github.com/zzzhhhhhgame-cmd/lanshare/releases)

**LanShare** 是一个纯局域网（LAN）点对点文件互传工具，专为 Windows / macOS 双持或多设备用户设计：
同一 Wi-Fi / 网线下的设备**自动互相发现**，网页里拖入文件即可秒传到另一台设备，**不经过任何云服务器、不上传互联网**。

- ✅ **自动发现**：基于 mDNS（Bonjour）广播，无需输入 IP
- ✅ **跨平台**：Windows、macOS、Linux 单文件可执行程序，无任何依赖
- ✅ **高速传输**：点对点直连，跑满局域网带宽（千兆网 ~110 MB/s）
- ✅ **文件夹整体发送**：保留目录结构
- ✅ **接收确认**：默认需对方点击"接受"（类似 AirDrop），也可开启自动接收
- ✅ **传输进度与历史**：双端实时进度、断点前自动重名保护、传输历史
- ✅ **仅限局域网**：接口只接受内网/本机来源的连接，防止被端口映射暴露到公网
- ✅ **命令行发送**：`lanshare send -to 设备名 文件...` 方便脚本调用

## 快速开始

### 1. 下载运行（推荐）

到 [Releases](https://github.com/zzzhhhhhgame-cmd/lanshare/releases/latest) 下载对应平台的压缩包：

| 平台 | 文件 |
|---|---|
| Apple Silicon (M1/M2/M3/M4) | `lanshare-*-darwin-arm64.tar.gz` |
| Intel Mac | `lanshare-*-darwin-amd64.tar.gz` |
| Windows 64 位 | `lanshare-*-windows-amd64.zip` |
| Linux 64 位 | `lanshare-*-linux-amd64.tar.gz` |

解压后运行 `lanshare`（Windows 为 `lanshare.exe`），会自动打开浏览器进入传输页面。

> **macOS 首次运行**：如果下载的二进制被 Gatekeeper 拦截，执行
> `xattr -d com.apple.quarantine lanshare` 后再运行。
> **Windows 首次运行**：Windows 防火墙弹窗请勾选"专用网络"并允许访问。

### 2. 用 Go 自行编译

```bash
go install github.com/zzzhhhhhgame-cmd/lanshare@latest
lanshare
```

或从源码构建：

```bash
git clone https://github.com/zzzhhhhhgame-cmd/lanshare.git
cd lanshare
go build -o lanshare .
```

## 使用方法

1. **两台设备都运行 LanShare**（确保连的是同一个局域网）
2. 各自浏览器会打开 `http://127.0.0.1:8612`
3. 在"附近设备"列表点击要发送到的设备
4. 把文件/文件夹拖进发送区，点击发送
5. 对方页面会弹出传输请求 → 点"接受"，文件即保存到对方 `下载/LanShare` 目录

找不到设备时（部分路由器开启 AP 隔离会屏蔽 mDNS 广播）：在"附近设备"卡片底部输入对方 `IP:端口` 手动连接。
本机地址可在"设置"里查看。

### 命令行发送（可选）

```bash
# 按设备名发送
lanshare send -to "Bruce的Mac" 报告.pdf 照片/

# 按 IP 发送
lanshare send -to 192.168.1.5 movie.mkv
```

## 端口与防火墙

| 用途 | 协议/端口 |
|---|---|
| 网页与文件传输（TCP） | `8612`（被占用时自动顺延，可在设置/启动参数修改） |
| 设备发现 mDNS（UDP） | `5353`（系统标准端口） |

Windows 首次运行允许"专用网络"访问即可；macOS 首次运行会在"允许传入连接"弹窗中点允许。

## 常见问题

**搜索不到对方设备？**
- 确认两台设备在同一局域网（同一 Wi-Fi / 路由器），且未开启路由器 AP 隔离
- 公司/校园网常屏蔽组播，改用"IP:端口 手动连接"
- 确认双方程序都在运行

**传输速度是多少？**
点对点直连，速度取决于局域网：千兆有线 ~110 MB/s，Wi-Fi 6 视信号 40~90 MB/s。

**传输的数据加密吗？**
不加密。LanShare 只接受局域网私网地址与本机的连接，不做公网暴露；在可信的家庭/办公网络中使用，与 AirDrop/局域网共享工具的常规信任模型一致。如需更严格的访问控制，欢迎提交 Issue 讨论。

**配置和接收的文件在哪里？**
- 配置：`%AppData%/lanshare/config.json`（Windows）、`~/Library/Application Support/lanshare/config.json`（macOS）
- 接收目录：默认 `下载/LanShare`，可在"设置"中修改

## 技术实现

- 单个 Go 二进制：HTTP 服务 + mDNS 广播/发现（[grandcat/zeroconf](https://github.com/grandcat/zeroconf)）+ 自有 UDP 广播发现（双通道，互为备份）
- Web UI 内嵌于二进制（`go:embed`），无外部静态资源
- 发送端浏览器 → 本机服务 → 对方服务，multipart 流式转发，不落临时盘
- 接收端边收边写并实时汇报进度；文件名做目录穿越/非法字符清洗

```
浏览器(拖文件) ──multipart──▶ 本机 LanShare ──流式转发──▶ 对方 LanShare ──▶ 落盘 + 通知
        ▲                  │  mDNS: _lanshare._tcp + UDP 广播(:8612)     │
        └──────── Web UI(设备列表/进度/历史/设置) ◀──────── 轮询 ──────────┘
```

## 开发与发布

本地交叉编译所有平台：

```bash
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o dist/lanshare .
GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o dist/lanshare .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o dist/lanshare.exe .
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=v1.0.0" -o dist/lanshare .
```

打 tag 发布：`git tag v1.x.x && git push origin v1.x.x`。

如需 GitHub Actions 自动构建发布，把 `scripts/release.yml` 复制到
`.github/workflows/release.yml`（要求登录令牌具有 `workflow` 权限）：

```bash
mkdir -p .github/workflows && cp scripts/release.yml .github/workflows/release.yml
```

## License

[MIT](LICENSE)
