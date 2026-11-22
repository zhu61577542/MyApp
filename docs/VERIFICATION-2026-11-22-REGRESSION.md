# Windows 与 macOS 实机回归

日期：2026-11-22。修复基于 `e9538cbb`，测试构建标记为 `0.1.5-alpha-validation`。源码文件和构建产物的 SHA-256 分别见 `evidence/2026-11-22-regression/source-files.json`、`artifacts.json`；本报告与修复一同提交。首轮历史结果保留在 `VERIFICATION-2026-11-22.md`。

## 范围

- macOS ARM64 实机与 Windows 10 19045 x64 实机，主从角色双向验证。
- Windows 所有新增程序、配置、缓存、临时文件和测试结果位于 `E:\test\MyApp-regression-20261122`，保留首轮目录。进程级环境变量重定向用户目录、应用数据和临时目录；未更改系统权限、开发者模式或防火墙。
- 构建和 Go 工具运行于 Docker；交叉编译的原生测试程序在对应实机执行。Linux 回归使用 Docker。
- 不执行 GUI 交互测试。Windows 原生桌面程序仅更新部署、检查版本和校验值，入口为测试目录下的 `Start-MyApp.cmd`。
- 用户暂未提供第三台实机，三机持续一小时验收暂缓。

## 修复与结果

| 项目 | 结果与证据（均位于下述证据目录） |
|---|---|
| Windows 剪贴板事务 | 同一系统线程完成打开、读写和关闭，文字与文件后端共享锁，写入提供有效所有者窗口，繁忙重试并响应取消。线程、文字/文件读写、8 个并发后端和取消测试通过：`010-clipboard-regression.json`。 |
| Windows 权限约定 | 普通 rwx 位在 Unix 恢复；Windows 将 owner-write 位映射为可写/只读属性，ACL 继承目标缓存目录。正常与只读目录、文件及修改时间通过：`073-transfer-final.json`。 |
| 失败提交恢复 | 元数据或重命名失败后恢复临时可写权限；进程重启加载时修复临时权限。Windows 持有不共享删除的目录句柄制造重命名失败，验证返回前回滚，再验证加载和重试：`073-transfer-final.json`。 |
| 路径与链接防护 | Windows junction 越界写入、状态恢复和取消防护通过。两项 Windows 真正符号链接测试因账户缺少权限明确跳过；对应 macOS 和 Linux 用例通过。 |
| macOS 文件剪贴板 | 初次互传复现发布后无法读取文件列表并断开会话：`060-mac-windows-exchange.json`。修复 Objective-C 类数组桥接，并同步写入完整文件路径属性列表；中文、空格、多条目和纯文字识别连续 30 次通过：`065-mac-clipboard.txt`。 |
| Mac 真实输入捕获 | A、左键和 Control+Option+Esc 紧急退出通过：`041-mac-physical-input.json`。 |
| Windows 真实输入捕获 | A、鼠标移动、左右键按下/释放、Ctrl+Alt+Esc 紧急退出通过：`084-windows-physical-input.json`。此前未完成左键操作的人工轮次不作为程序故障结论。 |
| Windows 原生输入 API | SendInput 到 Hook 的左右键按下/释放四种消息通过；测试 Hook 拦截注入事件，避免操作前台程序：`083-native-hook.json`。 |
| Windows 控制 Mac | 50 轮双向中文文字与双向目录通过，随后追加 150 轮、约 53.16 秒无重连：`080-*`、`086-*`。目录检查中文文件、内容、空文件与空目录，并等待读取到本轮文件剪贴板，避免旧缓存造成误判。 |
| Mac 控制 Windows | 50 轮双向文字、双向目录通过：`085-*`。`mac-reverse-controller.txt` 中之后的连接重置和重试发生于测试主动停止 Windows agent 后，不计为传输期间的重连。 |
| Windows 与 Linux 回归 | 100 轮双向文字与双向目录通过，无重连：`030-cross-platform-regression.json`。 |
| Mac 原生传输测试 | 全部通过，包括 POSIX 权限、符号链接防护和中断状态恢复：`087-mac-transfer.txt`。 |

全部证据位于 `docs/evidence/2026-11-22-regression/`。临时配对码已脱敏，不归档身份私钥或信任库。

## 自动检查

Docker 中 `go test -count=1 -race -tags "gui ci" ./...` 与 `go vet -tags "gui ci" ./...` 通过；这里的 GUI 标签使用无显示单元测试驱动。核心程序四目标交叉编译通过，Windows 原生 GUI 构建通过。

额外的 Windows/macOS 默认 `go vet` 对原生系统 API 的 `uintptr` 转 `unsafe.Pointer` 报告告警，原始输出保留在 `final-windows-vet.txt`、`final-macos-vet.txt`；不能称为这两项目标的默认 vet 全部通过。仅关闭 `unsafeptr` 检查后的其他分析通过，见对应 `*-native-api.txt`。这些边界还由原生实机测试验证。

## 尚未完成

1. 较早一轮两机互传发生一次心跳超时并自动重连，文字与目录最终成功：`075-mac-windows-exchange.json`、`076-diagnostics.json`。之后 080、085、086 均未复现，根因仍未确定。实时读循环同步执行原生输入注入、心跳与输入共享实时队列，均是需要采集耗时和积压数据核实的风险，不能据此认定为本次超时原因，也不能宣称该异常已修复。
2. 完整键盘布局、组合键、输入法、滚轮、拖拽、边缘切换及断网按键释放的实机矩阵尚未完成。基础探针验证紧急退出，不等同于全部控制状态机验收。
3. 三机一小时、真实 4 GiB 文件、十万小文件、磁盘不足、发送端断线偏移协商及剩余首版功能继续按实施计划推进。

上述缺口不影响确认本轮已验证的具体修复，但不满足第一版完整发布验收条件。

## 收尾

Windows 原生程序已更新到本轮测试目录。`088-final-audit.json` 确认该目录下测试应用进程已退出；Windows 轮询客户端停止结果见 `stopped.json`。Mac 服务、控制端和临时 HTTP 通道均在收尾时关闭。保留两端测试目录和归档证据，未自动删除首轮结果。
