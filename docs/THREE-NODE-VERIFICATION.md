# 0.1.3-alpha 三机与独立通道验证

日期：2026-09-23。此版本仍未完成整个产品，不能视为正式发布。

## 本轮实现

- 控制端配置 peers，接入设备管理器，主电脑最多连接两台子电脑。重复设备、超过数量、子电脑配置 peers 均被拒绝。
- Ctrl+Alt+1/2 切换输入目标，切换前释放旧目标按键；Ctrl+Alt+Esc 结束会话。
- 主电脑共享一个文字同步器和文件剪贴板同步器，接受子电脑内容后转发给另一台子电脑，不向来源回传；认证身份必须与子电脑文字来源一致。
- 实时输入/心跳和文字/文件使用独立 TLS 连接。两条连接各自认证并校验会话随机数，批量通道不接受输入事件。
- 文件任务阻塞时仍处理实时输入，断开后先释放按键，再等待批量任务退出。心跳间隔 100 ms、超时 750 ms；组内全部连接和捕获器准备后才发送启动心跳，避免建连阶段误超时。
- 任一连接失败会取消整组控制并保留失败原因；当前采取安全退出，不自动重连。

## 验证证据

全部开发工具运行在 Docker 中。

- `go test -count=1 -race ./...`：通过。
- `go vet ./...`：通过。
- `build/release.sh 0.1.3-alpha`：Windows x64、macOS ARM64、Linux x64/ARM64 构建打包通过。
- `TestHubThreeNodesRelayAndSwitch`：拒绝第四台设备、目标输入隔离、子电脑文字双向中转、文件中转、来源不回传。
- `TestSplitChannelKeepsInputAliveDuringBlockedFileWork`：文件处理阻塞超过心跳超时期间仍传递输入并保持连接；取消后释放不等待文件任务。
- `TestSplitHeartbeatReleasesWithinOneSecond`：隔离连接中静默断网在一秒内释放按键。
- `TestBulkChannelRejectsInput`、`TestBulkBindingRejectsDifferentSession`：拒绝错误通道输入和错误会话随机数绑定。
- `tests/e2e/run_three_nodes.sh`：三个 Xvfb 桌面、三套身份、两个子电脑配对、实际键盘切换、三端文字、子电脑间目录转发、紧急退出、子电脑离线后整组失败退出均通过。
- `tests/e2e/run_x11_cli.sh`：原有单目标参数用法、中文文字、目录和空文件流程回归通过。

实际桌面测试曾发现 Ctrl+Alt+F2 未到达应用，因此切换键改为 Ctrl+Alt+数字键；未放宽目标输入隔离断言。日志保存在 `work/three-nodes/`，当前有效桌面测试日志为 `e2e-split.log`、`e2e-legacy-cli.log`。

## 使用和限制

三台设备均需更新到本版本，旧 alpha 单连接会话不能与本版本混用。README 提供 peers 配置方法。

本轮的三机证据来自同一 Docker 容器中的三个隔离 X11 桌面，不等于三台实体电脑验收。Windows/macOS 原生运行、实体网络的一小时稳定性、真实大文件及十万文件仍待验证。

仍未完成：屏幕边缘切换、自动重连、跨连接文件续传、发送端完成确认、图形/托盘界面、Wayland 输入、缓存生命周期及元数据保真。文字仍限 128 KiB。独立连接消除了文件读写与输入共用接收循环的阻塞，但不能据此保证任意硬件负载下的实时性能。
