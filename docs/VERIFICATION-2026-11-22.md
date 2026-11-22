# 首次 Windows 与 Linux 双系统验证

测试日期为 2026-11-22。被测源码为 `0098240129aa826cdd73ef8323687375e9506780`，测试包标记为 `0.1.4-alpha`。

## 环境与范围

- Windows 10 22H2，内部版本 19045，x64 实体电脑。
- Linux 使用 Mac 上 Docker 内的 Alpine ARM64 和 Xvfb。Windows 担任控制端，Linux 担任受控端。
- Windows 新增程序、配置、缓存、临时文件和结果均存放于 `E:\test\MyApp-00982401`。进程级环境变量用于重定向用户目录和应用数据目录。
- 构建工具运行在 Mac 的 Docker 中。Windows 部署核心程序、原生桌面程序和测试二进制。
- 原生桌面程序完成版本检查，GUI 回归使用无界面测试驱动。

## 已通过

| 项目 | 证据 |
|---|---|
| 程序版本、配置和身份初始化 | `evidence/2026-11-22/summary.json` |
| 配对进程退出、后台保存、设置保存和主动终止回归 | `evidence/2026-11-22/021-nativegui-tests.json` |
| 发现、配对、信任和双向 TLS 集成 | `evidence/2026-11-22/031-integration-tests.json` |
| 中文、多行文字、Unicode 和 CF_HDROP 文件剪贴板读写 | `evidence/2026-11-22/051-nativeprobe.json` |
| 键盘、鼠标移动、按钮、滚轮注入及按下状态释放 | `evidence/2026-11-22/051-nativeprobe.json` |
| Windows 输入 Hook 安装及取消 | `evidence/2026-11-22/051-nativeprobe.json` |
| Windows 与 Linux 双向文字、目录和文件传输 | `evidence/2026-11-22/063-cross-platform.json` |
| 中文文件名、内容、空文件和空目录 | `evidence/2026-11-22/063-cross-platform.json` |
| 原生 Windows 程序部署、版本和校验 | `evidence/2026-11-22/070-install-native-gui.json` |

文件传输单元测试共 11 项，7 项通过，1 项权限元数据断言失败，3 项因创建符号链接权限不足而失败。各项输出保存在 `evidence/2026-11-22/041-transfer-tests.json`。

## 暴露的问题

1. 控制端出现 `Thread does not have a clipboard open.`，触发一次整组重连。随后恢复并完成双向传输。日志位于 `evidence/2026-11-22/080-final-audit.json`，需要复现和修复后再验收稳定性。
2. 权限保真测试期望 POSIX `0640`，Windows 返回 `0666`。需要明确跨平台权限约定，并据此验证实现和测试。
3. 当前 Windows 账户不能创建符号链接，三项安全用例保留待验证。测试未修改系统权限或开发者模式。

## 后续验收

- Windows 物理键盘捕获、组合键和本地紧急恢复。
- Windows 作为受控端，以及已授权 macOS 的输入和剪贴板矩阵。
- 文件中断恢复、真实 4 GiB 以上文件、十万文件、空间不足和冲突矩阵。
- 三台实体电脑连续一小时切换、网络故障和按键释放。
- 屏幕边缘切换、布局编辑、进度及首次安装使用流程。

以上项目继续按 `specs/MYAPP-001/PRODUCT.md` 的成功标准验收。

## 证据与收尾

原始有效运行输出已归档到 `docs/evidence/2026-11-22/`。构建产物 SHA-256 和大小记录在 `artifacts.json`。Windows 结果保留在 `E:\test\MyApp-00982401\results`。测试程序已退出，临时通道已关闭。该轮仅执行部署和验证，产品源码未改动。
