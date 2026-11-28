# 新会话交接：MyApp

更新：2026-11-28。仓库：`/Users/zhuguoqing/MyApp`；分支：`codex/bidirectional-crossing`。

## 从这里继续

用户准备结束当前长会话，在新会话继续开发。当前双向改造采用五阶段计划，见 `specs/MYAPP-001/PEER-IMPLEMENTATION.md`；不要与更早 `IMPLEMENTATION.md` 的首版八阶段编号混淆。

阶段1、2已验收。阶段3的双向跨屏、快速触边、CapsLock和速度体验已由用户认可；本轮用户另明确回答第三阶段四项收尾实机核查“四项均通过”：速度设置保存/重启/恢复默认、两端实体接管、急停及暂停恢复、显示器变化后的恢复。详细结果见 `docs/PEER-STAGE3-ACCEPTANCE.md` 和 `docs/evidence/2026-11-28-stage3-closeout/`。

**下一项是第四阶段的产品规格与实现，不要重新开始排查已经通过的T0跨屏问题，也不要直接跳到第五阶段。** 每个阶段完成后必须配合用户进行一次带UI的人工核查，通过后才进入下一阶段。本轮收尾只补回归测试与文档，不改变产品代码或重发新版本；继续使用0.1.17。

## 用户要求与工作约束

- 中文沟通；独立判断，区分已验证事实和推测。完成工作后询问是否需要补充验证。
- 所有Go编译、测试、gofmt、vet在Docker中运行；编译好的原生测试二进制可在Mac/Windows执行。Python必须使用虚拟环境，默认`conda activate data`。
- Windows所有文件、工具、软件和测试只能位于`E:\test`。不改防火墙，不自动修改系统键鼠偏好；保留既有身份、信任、配对、布局与速度设置。
- 不做GUI自动化。GUI操作、真实跨屏、键鼠捕获探针由用户配合；不要自动操纵、隐藏实体键鼠，也不要擅自退出/杀死用户运行中的MyApp。
- 信任关系和权限就绪后自动跨屏，无需额外控制按钮；两端实体键鼠都能发起。内部显示器接缝不跨机。
- macOS权限范围已获用户同意：程序登记并请求，由用户确认；可重置MyApp键鼠权限，明确本地网络权限不能由程序清除。不要承诺绕过系统确认。
- 用户已授权修复、验证后提交并推送。不要反复询问提交权限；提交加`Co-Authored-By: Oz <oz-agent@warp.dev>`。
- 仓库存在`.codegraph/`，定位/理解代码先使用CodeGraph；不要重新索引。当前开发可沿用规格驱动与规格核对流程。

## 当前版本与启动

最新应用：**0.1.17-alpha-stage3**，应用源码提交`b00a2722`；后续提交为验收记录、回归测试和交接文档。新会话先执行`git status --short`、`git log -5 --oneline`核对最终提交及工作区。

- Mac应用：`/Users/zhuguoqing/MyApp/work/peer-stage3-speed-ui/MyApp.app`。
- Mac包：同目录`MyApp-macOS-arm64.zip`。
- Windows包：同目录`MyApp-Windows-x64.zip`，已传送并校验，解压在`E:\test\MyApp-0.1.17-alpha-stage3\app`。
- Windows启动：`powershell.exe -NoProfile -ExecutionPolicy Bypass -File "E:\test\MyApp-0.1.17-alpha-stage3\app\Launch-in-E-test.ps1"`。
- Windows沿用profile：`E:\test\MyApp-0.1.6-alpha-edge\app\profile`；不要因目录版本旧而删除。
- Mac有ad-hoc完整性签名，没有Developer ID与公证；首版允许此分发方式，认证签名不属于当前阻断。
- SHA-256及传送回执：`docs/evidence/2026-11-28-stage3-speed/hashes.json`、`windows-delivery.json`。

本轮已确认两端实际运行0.1.17。PID会变化，不要把历史PID作为停止进程的依据。

## 日志与联调通道

- Mac日志：`/Users/zhuguoqing/Library/Application Support/MyApp/logs/myapp.log`。
- Windows日志：上述profile下`AppData\Roaming\MyApp\logs\myapp.log`。
- 记录状态、控制代次、停止原因、耗时、输入数量与倍率；不记录按键内容/键码、剪贴板正文、原始轨迹或屏幕录像。
- `keyboard_injected`表示注入器成功处理协议事件，包含有意抑制的Caps释放/重复，不等于原生事件数或显示字符数。
- 日志中普通服务诊断也可能带`[error]`前缀。不能仅凭前缀判断故障；应结合结构化事件和用户现象。最近有限窗口有少量“控制权正在使用或等待恢复”的申请拒绝，未与每次用户操作对齐；保留给第五阶段评估，不宣称零拒绝或100%成功率。

最后确认的局域网IP：Mac`192.168.1.8`，Windows`192.168.1.5`；新会话重新核对，不能假定永久不变。Windows没有可直接使用的SSH/WinRM通道，使用用户启动的临时客户端：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "E:\test\MyApp-stage1-acceptance-20261127\bootstrap.ps1"
```

Mac桥接程序：`work/windows-regression-20261122/server.cjs`，监听18765，令牌文件为同目录`token`，不得打印或写入文档。`jobs/`投递UTF-8 JSON任务，`results/`保存回执。先检查服务/端口及客户端期限，不要重复创建监听器。客户端约两小时到期，必要时才请用户重新运行；此通道只是联调工具，不是MyApp使用前提。

最新编号：172运行状态/速度配置，173第三阶段原生收尾回归；下个任务从174起并先检查是否占用。任务外层结果含`stdout/stderr/exitCode/timedOut`；解析stdout取得实际JSON。下载通用包路由`package.zip`来自`work/windows-regression-20261122/MyApp-windows-test.zip`，目前内容为收尾测试包，不是GUI包；传GUI前复制正确发行ZIP，并在覆盖前确认没有未完成下载任务。旧`gui.zip`路由不要当作最新版。

Windows回归始终显式禁用实体Hook测试：`MYAPP_WINDOWS_HOOK_TEST=0`。只读或替身测试不能被描述为实体输入验证。新会话需采集日志时，从最新文件长度建立基线；不要把旧偏移后的全部日志算作一次新试验。

## 第四阶段：下一步工作

先补充本阶段产品/技术规格并核对现有实现，再按原生清爽风格实现：

1. 去掉面向用户的固定“主电脑/子电脑”角色选择，两端都是可发送、可接收的平等设备。
2. 迁移现有拨号/监听配置和互反布局，保留身份、信任、配对、位置、速度偏好及旧版兼容边界；不要只改标签，也不要重建信任来回避迁移。
3. 统一设备页、设置、配对向导、菜单栏/托盘和错误提示。部分动态来源/目标显示已经实现，先复用；明确显示本机控制对方、对方控制本机、暂停、权限或连接阻断。
4. 完善双端包、升级与使用说明，并配合用户人工验收：旧配置升级、新设备配对、双向控制、状态显示、设置和托盘。验收通过后再进入阶段5。

## 第五阶段及首版剩余清单

- 完整实机故障矩阵：断网重连、退出重启、休眠唤醒、权限变化、双方同时操作、跨屏与文件传输并行、显示器热变更、长时间运行；确认无卡键/残留隐藏光标，旧控制不能复活。
- 记录性能分段、队列/心跳情况，评估恢复期拒绝和诊断日志等级/体积。状态握手耗时不等于端到端光标呈现延迟。
- 原首版文档仍列有发送端断线偏移协商（接收进度已有，重新连接的发送续传未补齐）、缓存引用/过期清理、大于4GiB实文件、十万小文件、空间不足与冲突矩阵。实施前核对当前源码，不按历史勾选盲目重做。
- 安装、升级、卸载与分发验收；Windows代码签名属于可选发布增强，Mac首版不强制Developer ID/公证。
- 只有Mac和Windows两台实机；三机持续一小时验收因缺第三台实体电脑暂缓。Linux完整实机/Wayland能力范围不属于当前双端收尾，不将X11容器通过说成全部Linux桌面通过。

## 代码与构建入口

- 规格/状态：`specs/MYAPP-001/PEER-PRODUCT.md`、`PEER-TECH.md`、`PEER-STAGE2-TECH.md`、`PEER-STAGE3-TECH.md`、`PEER-IMPLEMENTATION.md`；UI沿用`UX.md`。
- 控制与输入：`cmd/myapp/desktop_peer.go`、`desktop_activity.go`、`desktop_edges.go`、`desktop_runtime.go`；`internal/ownership`、`internal/peercontrol`。
- 界面/迁移：`internal/nativegui/app.go`、`settings.go`、`pages.go`、`setup.go`、`pairing.go`、`service.go`；`internal/config`、`internal/desktop/session.go`。
- 速度：`internal/input/settings.go`、`scaler.go`、`internal/nativegui/input_settings.go`。本机设置只影响本机键鼠控制对方，下一租约生效；默认100%，不是自动复制目标系统加速曲线。
- 几何与平台：`internal/pointer/*`、`internal/platform/windows/input/physical_windows.go`与`raw_windows.go`、`internal/platform/darwin/input/injector_darwin.go`。
- 收尾回归：`cmd/myapp/desktop_closeout_test.go`、`internal/desktop/input_settings_restart_test.go`，复用真实Link与替身后端，不操作真实键鼠。

T0修复要保留：Windows实体位置与同次事件绑定；Hook拟落点越界时以合法系统最终位置复核，不能直接把任意无效点投影到边缘。Windows统一PMv2物理坐标，Mac用CoreGraphics全局逻辑坐标，不重复乘Retina倍数。DPI不是已证明的唯一根因。CapsLock按压只翻转一次，释放/清理不额外翻转；Mac滚轮按行单位注入。

常驻开发容器`myapp-ui-dev`，仓库挂载`/src`，Go 1.26.1；使用已有Go模块与编译缓存。`attu`是无关容器，不停止。常用检查：

```bash
docker exec -w /src myapp-ui-dev go test -race ./...
docker exec -w /src myapp-ui-dev go vet ./...
docker exec -w /src myapp-ui-dev go test -tags 'gui ci' ./internal/nativegui
```

原生GUI构建使用本地镜像`fyneio/fyne-cross:1.3-windows`、既有Mac SDK `work/desktop-ui-validation/MacOSX15.2.sdk`与容器内Zig工具链；不要重复下载大型SDK。0.1.17构建产物和校验清单仍在`work/peer-stage3-speed-ui/`。临时Windows构建容器和源树临时syso已清理，后续构建应结束后同样清理。

Git推送曾受当前网络直连限制，本会话已验证可用SSH 443加本地HTTP代理127.0.0.1:7900；代理端口需重新确认，不是外部获取的免费代理。可沿用：

```bash
git -c remote.origin.pushurl=ssh://git@ssh.github.com:443/zhu61577542/MyApp.git \
  -c 'core.sshCommand=ssh -o HostKeyAlias=github.com -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10 -o "ProxyCommand=nc -X connect -x 127.0.0.1:7900 %h %p"' \
  push origin codex/bidirectional-crossing
```

不要打印凭据或更改全局Git配置来绕过网络问题。
