# MyApp

MyApp 是面向 Windows、macOS 和 Linux 的局域网键鼠与剪贴板共享软件。第一版支持最多 3 台设备，由配置指定主电脑。

当前为 `0.1.3-alpha`。已接通主电脑同时连接两台子电脑、快捷键切换、三端文字同步和经主电脑转发的子电脑间文件复制。键鼠/心跳与文字/文件使用两条独立的认证 TLS 连接。三端 Linux X11 CLI 测试已验证切换、文字、目录、紧急退出及子电脑离线时整组退出。屏幕边缘切换、自动重连、系统托盘界面、Wayland 输入 portal、跨连接续传和 Windows/macOS 实体机矩阵仍在实施中。准确状态位于 `specs/MYAPP-001/IMPLEMENTATION.md`。

## Docker 开发

构建开发镜像：

```sh
docker build -t myapp-dev:local -f build/docker/Dockerfile.dev .
```

运行测试：

```sh
docker run --rm --network none -v "$PWD:/src" -v myapp-go-cache:/go -w /src myapp-dev:local test -race ./...
```

构建当前平台无关核心：

```sh
docker run --rm --network none -v "$PWD:/src" -v myapp-go-cache:/go -w /src myapp-dev:local build ./cmd/myapp
```

开发工具只存在于 Docker 镜像中，宿主机不需要安装 Go。

## 当前命令行流程

每台电脑先初始化身份和配置，再用 `pair listen` / `pair connect` 完成双端确认。受控电脑运行：

```sh
myapp serve
```

主电脑运行：

```sh
myapp control -peer <目标设备ID> -addr <目标IP:24800>
```

进入控制后按 `Ctrl+Alt+Esc` 收回本地控制。macOS 需要先授予辅助功能和输入监控权限；Linux 键鼠控制当前使用 X11。

三机使用时，先分别配对主电脑与两台子电脑，再在主电脑配置中加入真实设备 ID 和局域网地址：

```json
"peers": [
  {"id": "第一台子电脑的设备ID", "address": "192.168.1.20:24800"},
  {"id": "第二台子电脑的设备ID", "address": "192.168.1.21:24800"}
]
```

这是配置片段，需要加入已有配置对象。子电脑分别运行 `myapp serve`，主电脑运行 `myapp control`（无需 peer/addr 参数）。默认控制列表中的第一台；按 `Ctrl+Alt+1` / `Ctrl+Alt+2` 切换，按 `Ctrl+Alt+Esc` 退出。三个节点可通过主电脑同步文字及文件；主电脑必须在线。`max_devices` 包含主电脑，最多为 3。任何子会话失败时整组退出并释放按键，当前不会自动重连。

所有节点应使用 `0.1.3-alpha`：拆分通道及启动就绪握手不兼容旧 alpha 会话。文件传输会先缓存再发布剪贴板，尚无进度界面和发送端完成确认；长文字目前限 128 KiB。

## 发布包

`dist/` 包含 Windows x64、macOS ARM64、Linux x64/ARM64 包及 `SHA256SUMS`。macOS 第一版按要求直接提供未签名、未公证的 Apple Silicon `.app`。

`0.1.2-alpha` 修复输入重放、剪贴板版本与去环错误、文件缓存越界和内部状态文件重名，并加入握手超时及完整的会话退出等待。接收端恢复状态移至缓存根目录的 `.state-<ID>.json`；旧 alpha 未完成传输不会自动迁移，请重新复制源文件，旧缓存不会被自动删除。当前缓存复制不恢复原始修改时间和目录权限，相关元数据保真仍待完善。
