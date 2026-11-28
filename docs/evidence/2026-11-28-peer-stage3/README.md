# 第三阶段交付证据

版本0.1.13-alpha-stage3，最终源码dbaa0cbb，核心接入提交e968dc6a。

已实现Mac注入来源标记和过滤、双端不拦截的实体监听、完整token绑定的本机接管门禁、物理按键保护、平台就绪及互反位置确认、Windows整组桌面来源边界与实际返程落点，并补充实际控制方向状态。完整去主副界面和配置迁移仍为第四阶段。

回归包括Hook通知早于系统光标更新、控制状态切换后的新实体移动、监听就绪空洞、监听退出、已按下修饰键/首个本机按键保留、旧回调、双向触边、内部接缝、被控态/恢复后不反弹、Windows扩展屏返程，以及旧会话/旧修订/畸形位置拒绝。具体见测试源码和日志。

- 全项目race/vet、GUI编译及无窗口状态摘要单元检查：final-checks.log。
- Mac输入过滤/初始按键状态/错误不含原始键码：macos-input-tests.log；原生双向运行时替身场景：macos-runtime-tests.log。
- Windows三组原生回归：142为首轮结果，145为最终结果；均physicalInput=false，未启用真实Hook注入测试。
- 最终GUI传送/版本/ZIP与EXE校验/GUI子系统/启动脚本语法：144回执。最终版本commit=dbaa0cbb，未由助手启动新GUI。
- SHA256SUMS为最终双端GUI包及测试包校验值；session.json保存两端启动路径和真实程序日志字节起点。

所有Go编译/测试均在Docker，已编译的原生替身测试分别在Mac和Windows执行。使用现有Mac SDK和MinGW镜像，没有新增SDK；临时Windows编译容器与资源对象已删除。Windows下载、检查、程序和日志均在E:\test，保持原身份与信任配置。

人工验收尚未开始；未做GUI自动化，不以后台PASS代替实体操作效果。待用户按docs/PEER-STAGE3-ACCEPTANCE.md逐项核查后登记验收；第四阶段未开始。仅记录程序状态、错误和用户反馈，不记录原始输入、剪贴板内容或录屏。不支持按键的错误已去除具体键码。
