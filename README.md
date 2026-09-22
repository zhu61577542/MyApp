# MyApp

MyApp 是面向 Windows、macOS 和 Linux 的局域网键鼠与剪贴板共享软件。第一版支持最多 3 台设备，由配置指定主电脑。

当前为 `0.1.0-alpha`。已实现配对、TLS 会话、三平台键鼠适配器、文字剪贴板、文件与目录流式传输，以及命令行 `serve`/`control` 运行入口。系统托盘界面、Wayland 输入 portal、跨连接续传和完整实体机矩阵仍在实施中。准确状态位于 `specs/MYAPP-001/IMPLEMENTATION.md`。

## Docker 开发

构建开发镜像：

```sh
docker build -t myapp-dev:local -f build/docker/Dockerfile.dev .
```

运行测试：

```sh
docker run --rm --network none -v "$PWD:/src" -w /src myapp-dev:local go test -race ./...
```

构建当前平台无关核心：

```sh
docker run --rm --network none -v "$PWD:/src" -w /src myapp-dev:local go build ./cmd/myapp
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

## 发布包

`dist/` 包含 Windows x64、macOS ARM64、Linux x64/ARM64 包及 `SHA256SUMS`。macOS 第一版按要求直接提供未签名、未公证的 Apple Silicon `.app`。
