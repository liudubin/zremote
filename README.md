# ZRemote — Windows 4K/120fps 远程桌面主机

一个单 exe 的 Windows 远控主机端：**DXGI 桌面复制捕获 + NVENC 硬件编码 + WebSocket 推流**，
内置网页客户端（WebCodecs 硬件解码），手机/电脑浏览器扫码即用。为 4K 120Hz 屏幕设计。
另附 iOS 原生壳 App（WKWebView），可打包成 IPA 安装到越狱/巨魔设备。

## iOS App（IPA）

`ios/` 是一个极简 WKWebView 壳工程（xcodegen + Swift，约 600 行），把网页客户端装成独立 App：

- 首屏填 **主机地址 + 访问令牌**（支持粘贴完整链接自动拆分、**扫码** zremote.exe 控制台二维码），
  保存在本机 UserDefaults，之后启动直达远程桌面
- **自签证书放行**：仅对已保存的主机信任其自签证书（内网自用主机），其它站点走系统默认校验
- 右下角 ⚙ 悬浮钮：断开重设 / 刷新；主机不可达时顶部红条提示重试
- 横竖屏全支持（对应网页的三端自适应形态）、iPhone/iPad 双设备族、深色主题
- 系统要求 iOS 15+（编译目标）/ **16.4+（运行时，WebCodecs 硬解）**

**构建 IPA**（本地需 macOS + Xcode；或直接用仓库的 GitHub Actions）：

```
gh workflow run ios.yml          # 手动触发; push tag v* 自动构建并发布 Release
gh run download -n ZRemote-ios -D dist
```

产出未签名 IPA（ldid 假签）：**越狱（RootHide/AppSync Unified）或 TrollStore 直装**；
无越狱设备可用 SideStore/Feather/爱思助手等自签工具签名后安装。

仓库：<https://github.com/liudubin/zremote>（Actions 的 macOS 构建需公开仓，代码不含任何令牌）

```
┌──────────┐   H.264@120fps   ┌────────────┐   WebCodecs 硬解
│ zremote  │ ───────────────▶ │ 任意浏览器  │ ─────────────▶ 画布渲染
│   .exe   │ ◀─────────────── │ (内置页面)  │ ◀───────────── 鼠标/键盘/触屏
└──────────┘  SendInput 注入   └────────────┘
```

## 实测性能（i7-13700HX + RTX 4060 / 2560×1600）

| 指标 | 数值 |
|---|---|
| 编码 | NVENC h264, CBR 60Mbps, `tune=ull` 零延迟, **119~120 fps** |
| 解码（浏览器） | 硬解 **120 fps**，解码耗时 <1ms |
| 端到端延迟 | 局域网典型 30~80ms |
| CPU 占用 | 捕获+编码全在 GPU, CPU 近乎无感 |

外接 4K120 显示器时自动按 3840×2160@120 捕获（原生分辨率跟随显示器）。

## iOS / iPadOS 专项适配

- **Safari 16.4+ / 主屏幕 WebApp** 全支持（WebCodecs 硬解）；建议用 HTTPS 链接添加到主屏幕，
  独立全屏运行（状态栏沉浸 `black-translucent`）。
- **硬解自动降档**：iPhone/iPad 的 H.264 硬解上限低于 4K120，首次连接自动切 **1080p·60fps**
  保障流畅（一次性，⚙ 菜单可手动调回/调高，新设备机型强可先试 1440p）。
- **系统手势隔离**：拦截 Safari 双指缩放（gesture* 事件）、禁用长按放大镜/呼出菜单
  （`-webkit-touch-callout`）、输入框 16px 防聚焦自动放大页面。
- **键盘弹起适配**：visualViewport 跟随布局，竖屏底部抽屉自动避开软键盘。
- 屏幕常亮（Wake Lock 16.4+）、安全区（刘海/Home 横条）全适配；iPhone 横屏为沉浸模式。
- WKWebView 壳 App（如自研壳）默认拒绝自签证书，需在壳内放行或改用 Safari/PWA。

> 分辨率缩放说明：缩放走 `hwdownload→软件缩放→nv12` 链（ddagrab 输出 d3d11 帧，
> `scale_cuda` 不兼容）；无缩放时 NVENC 保持零拷贝纯硬路径，4K120 无额外开销。

## 安装为独立 App（PWA，不依赖浏览器/ZCode）

ZRemote 自带 PWA 支持，可以"装"成独立应用，全屏运行、有自己的图标、点开直连主机：

- **Android（Chrome/Edge）**：用 **HTTPS 链接**（`https://主机IP:48443/?k=令牌`）打开 → ⚙ 菜单点
  **"安装到桌面"**（或浏览器菜单"安装应用"）。Service Worker 需要安全上下文，所以必须走 HTTPS。
- **iOS（Safari 16.4+）**：打开 HTTPS 链接 → 分享菜单 → **添加到主屏幕**（图标自动就位，
  以 standalone 全屏模式运行，WebCodecs 硬解可用）。
- **Windows（Chrome/Edge）**：地址栏右侧安装图标，或 ⚙ 菜单"安装到桌面"。
- 安装后的 App 内置了带令牌的启动地址，点击图标即连；**换令牌（-newtoken）后需用新链接重新添加**。

实现：服务端动态生成 `/manifest.webmanifest`（start_url 内嵌令牌）、`/icon-192.png`、`/icon-512.png`
（Go 程序化绘制的显示器+播放图形，maskable 全出血）、`/sw.js`（只提供安装资格与离线导航提示，
不缓存任何数据，不影响推流延迟）。

## 快速开始

1. 目录结构（`bin/`）：
   - `zremote.exe` — 主程序
   - `ffmpeg.exe` — BtbN GPL 构建（含 ddagrab/NVENC/QSV/AMF），与本 exe 同目录即可
2. 双击运行（或命令行），控制台打印访问链接 + 二维码：
   ```
   局域网  : http://192.168.x.x:48080/?k=XXXXXXXX
   HTTPS   : https://192.168.x.x:48443/?k=XXXXXXXX   ← 手机推荐
   ```
3. 手机扫码 / 电脑浏览器打开链接。**链接里的 `?k=` 令牌就是访问凭证**，不要外传。
4. 页面默认**输入锁定**（防误触），点击右上角 🔒 或底部横幅解锁后即可控制。

> 首次运行 Windows 防火墙会弹窗，勾选"专用网络"允许。
> 手机用 HTTPS 链接时，自签名证书警告选择"继续访问/高级→仍要访问"。

## 控制方式

### 三端自适应

页面按「指针类型 + 屏幕尺寸」自动切换三种形态（`body` 上的 `mode-*` 类）：

| 形态 | 判定 | 布局与交互 |
|---|---|---|
| **电脑端** | 鼠标指针 | 完整状态栏 + 底部工具栏；键盘鼠标直通；⚙ 里可切**相对鼠标（指针锁定，游戏用）**，点击画面锁定、Esc 退出 |
| **手机端** | 触屏且短边 <600px | **竖屏**：画面在上、键盘 4 行网格在下、"🔄 横屏体验更佳"提示（点击尝试全屏+锁横屏）；**横屏**：沉浸模式——画面铺满，左上悬浮工具胶囊、右上帧率/码率胶囊、底部"⌨ 键盘"唤出抽屉（点画面收回）；屏幕常亮（Wake Lock）；iOS 伪全屏兜底 |
| **平板端** | 触屏且短边 ≥600px | 与电脑端同布局，按钮加大到 ≥44px 触控目标，手势与手机一致 |

手机/平板触屏手势（两种形态通用）：

- 单指移动 = 光标 · 点按 = 左键 · 双击 = 双击 · **双击后按住拖动 = 拖拽**（拖文件/选文字）
- 长按 = 右键 · 双指上下 = 滚轮 · 双指点按 = 右键
- 底部虚拟按键：Ctrl/Alt/Shift/Win（粘滞）、Esc/Tab/Enter/Space/Del/方向键/退格
- ⚙ 菜单：任务管理器 / Alt+F4 / Win+L / Win+D / Win+E、**中文文本发送**（输入法打字→发送，KEYEVENTF_UNICODE 注入）
- 相对鼠标模式下，手指滑动直接发相对位移，游戏视角更跟手

**画质热切换**（⚙ 菜单）：分辨率（原生/2K/1080p/720p）、码率（20~100Mbps）、帧率（60/120），
改动即时生效并持久化到 `zremote.json`。断线自动重连（1/2/4/8s 退避），网络恢复立即重连。

## 命令行参数

```
zremote.exe [-port 48080] [-data 目录] [-newtoken] [-v]
```
| 参数 | 说明 |
|---|---|
| `-port` | HTTP 端口，HTTPS 固定为 port+363 |
| `-data` | 配置/证书目录（默认 exe 同目录） |
| `-newtoken` | 重新生成访问令牌 |

`zremote.json` 可手动改：`bitrate_mbps / fps / scale_w / scale_h / monitor / encoder / gop_sec / token`。
`monitor` 选捕获哪块屏幕（ddagrab output_idx），`encoder` 可强制 `nvenc/qsv/amf/x264`（默认自动回退：
NVENC → Intel QSV → AMD AMF → libx264 软编）。

## 工作原理

- **捕获**：ffmpeg `ddagrab` 滤镜走 DXGI Desktop Duplication，D3D11 纹零拷贝直达 NVENC，
  帧率=显示器刷新率（静止画面自动复用帧，几乎零码率）。
- **编码**：`h264_nvenc -preset p4 -tune ull -rc cbr -bf 0 -delay 0`，1 秒 GOP 保证
  新客户端 1 秒内出画面；关键帧率与码率由服务端钳制，无注入面。
- **传输**：服务端把 Annex-B 流切成访问单元（AU），二进制 WebSocket 帧广播；慢客户端
  主动丢帧保延迟；客户端解码队列 >12 帧时丢增量帧。
- **控制**：控制消息走同一条 WS（JSON），服务端 `SendInput` 注入：绝对/相对鼠标
  （多屏自动切换虚拟桌面坐标系）、键盘扫描码（含扩展键）、`KEYEVENTF_UNICODE` 文本。
- **安全**：8 位随机令牌鉴权（页面与 WS 都校验）；自签名 ECDSA 证书（SAN 自动覆盖本机
  IP，IP 变化自动重生成）；无人观看 3 秒后停止推流。

## 已知限制

- `Ctrl+Alt+Del` 属安全注意序列无法注入（用 Ctrl+Shift+Esc 代替）；UAC 提权弹窗与锁屏
  界面捕获为黑屏且无法输入（同所有用户态远控，需装服务才能穿透）。
- 独占全屏游戏可能让 ddagrab 拿不到画面（改为无边框窗口即可）。
- HDR 开启时画面会偏灰，建议远控时关闭 HDR。
- 相对鼠标模式（游戏视角）未做指针锁定抓取，移动类 FPS 体验有限，桌面/办公/回合制完全够用。
- 无音频（路线图：WASAPI loopback + Opus）。

## 从源码构建

```
cd zremote
go build -trimpath -ldflags "-s -w" -o bin/zremote.exe .
```
依赖：Go 1.22+（`coder/websocket`、`skip2/go-qrcode`，`GOPROXY=https://goproxy.cn`）、
ffmpeg.exe 放 bin/（BtbN FFmpeg-Builds `ffmpeg-master-latest-win64-gpl.zip`）。

## 调试

设置环境变量 `ZREMOTE_DBG=1` 启动，会打印每条鼠标注入与执行后的光标物理坐标。
