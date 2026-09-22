# 阶段 3～7 验证记录

日期：2026-09-22

## 已通过

- Docker 内执行 `go test -race ./...` 与 `go vet ./...`。
- Windows x64、macOS ARM64、Linux x64、Linux ARM64 全源码构建通过。
- `net.Pipe` 双端会话通过输入送达、断开释放和目录分块传输测试。
- Linux Xvfb 使用产品 X11 适配器完成捕获与注入闭环。
- Linux Xvfb 完成中文多行文字剪贴板读写闭环。
- Linux Xvfb 完成带中文和空格文件名的 `text/uri-list` 文件剪贴板闭环。
- 两个隔离 X11 桌面使用独立身份完成短码配对、TLS 会话、主端捕获和受控端按键注入端到端测试。
- macOS ARM64 被动探针加载 CoreGraphics；当前辅助功能和输入监控权限均为未授权，探针未触发授权请求。
- macOS 文件剪贴板只读探针加载成功，只输出变更号、是否存在文件和数量，不输出路径。
- 生成四个 `0.1.0-alpha` 分发包及 SHA-256 清单，并核对包内路径。

## 尚未通过，不能据此宣称发布完成

- Windows 实体机的 Hook、SendInput、Unicode 剪贴板和 CF_HDROP 测试。
- 用户授权后的 macOS Event Tap、CoreGraphics 注入及 File URL 写入测试。
- Wayland RemoteDesktop portal/libei 输入实现；当前只检测并明确报告不可用。
- 三台实体电脑的一小时连续切换和断网测试。
- 跨连接文件续传偏移协商、4 GiB 实体文件和十万小文件矩阵。
- 系统托盘界面和 Windows 可选代码签名。

macOS 第一版按产品要求分发未签名、未公证的 Apple Silicon `.app`，Apple 认证和证书不属于待验证项。
