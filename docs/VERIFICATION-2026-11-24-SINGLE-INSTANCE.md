# 0.1.9-alpha 跨屏修复、单实例与 Windows 托盘

用户本轮反馈：鼠标完全无法跨屏、同一台电脑不能重复运行主程序、Windows 缺少托盘图标。用户随后实机确认新版鼠标跨屏“非常完美”。本轮没有把固定 controller/agent 架构改成双向对等控制；Windows 本机实体鼠标反向发起控制 Mac 是用户新提出的下一步产品设计问题。

## 查明的事实与边界

- Mac保存的位置是Windows在左侧；用户确认左右都尝试过，因此不能把故障归因于移动方向。Mac当前只有一块1470×956主屏，旧包只读指针探针也能正常读取源端几何。
- 历史日志包含多次GUI启动、Windows监听端口冲突、剪贴板哈希错误，以及用户撤销信任期间的证书拒绝。重新配对后有连接成功记录；这些历史事实不能直接证明当前失败的唯一根因。初始检查时两端主程序均未运行。
- 原实现把跨屏失败仅保存在瞬时Snapshot.Error；指针读取失败、源端几何缺失可能被静默忽略，界面又仅凭远端能力显示就绪。退出后缺少可追溯证据，本轮已补齐。

## 实现

1. GUI初始化前取得单实例锁，重复打开仅通知现有窗口显示并获取焦点；共享服务另有防重锁，避免重复监听。Windows使用命名Mutex和Event；Mac/Linux使用用户缓存目录中的flock及0600 Unix socket。实例名独立于包和配置目录，0.1.9及之后版本共享；升级前须先退出没有锁的旧版本。关闭、异常退出均能释放，遗留socket在持锁后清理。一个主程序允许它管理必要的共享服务子进程。
2. Windows托盘不再传SVG。项目所用Fyne 2.6.3的toOSIcon会用image.Decode再转ICO，SVG无法解码；现使用由既有品牌SVG生成的64px PNG，同时用于Windows应用图标。保留打开、暂停、权限及退出菜单。Mac菜单栏资源保持原有方式。
3. 初次发送完整拓扑优先于旧主屏消息，避免初始化能力升级误取消首次切换；旧ScreenInfo JSON及可选消息兼容保留。
4. Mac pbpaste/pbcopy固定UTF-8 locale。Entry拒绝非法UTF-8，避免JSON替换非法字节后哈希改变；本地非法编码或超出大小限制的文本跳过当前修订，不终止键鼠连接。远端校验保持严格。此缺陷已用合成字节复现，未读取用户剪贴板；不能反推历史实际文本就是该编码。
5. 新增源端EdgeReady、EdgeStatus、EdgeError：监视不可用、指针读取失败、等待远端布局及捕获失败均有可见状态；捕获错误不因连接状态刷新消失。源端就绪后才显示可跨屏。状态变更去重并写入既有日志，不记录指针坐标、按键或剪贴板内容。
6. 新增只读desktop-diagnostics，查询当前调用进程的捕获/注入预检、指针是否可读及屏幕矩形；不安装Hook、不读取剪贴板、不隐藏/移动指针。Mac升级授权请求批次为4，每次升级仅自动尝试一次，最终仍需系统确认。

## 验证

证据在 docs/evidence/2026-11-24-single-instance。

- Docker全量go test -race -timeout=60s ./...、go vet ./...通过；GUI编译/vet检查通过。
- 新增后台测试覆盖独立进程重复启动阻止、唤起通知、正常退出及崩溃后的锁释放、重复GUI在创建窗口前返回、重复服务在加载其他配置前拒绝。
- 图标PNG解码及ICO转换、服务未就绪不得显示就绪、失败不被连接状态清除、指针读取失败不得静默、完整拓扑先于临时主屏、非法UTF-8及后续合法文本继续同步等回归通过。
- Mac原生cmd/instance/clipboard后台测试通过。Windows任务123 exit 0、无超时、无stderr：instance/cmd/app/clipboard测试及限定的资源/实例入口测试通过；未打开测试GUI。
- 一次额外的全量gui/ci无界面单测遇到既有配对窗口测试默认主题缺少粗体等宽字体。测试夹具改用实际desktopTheme后，全量gui/ci单测和vet通过。该修正仅在测试文件，没有改变本轮已生成程序。没有在用户桌面执行GUI自动化。
- 双端CGO GUI编译通过，Mac .app ad-hoc签名及strict校验通过，无Developer ID/公证；版本0.1.9-alpha，嵌入commit=preview。
- Mac只读诊断由工具启动的进程预检三项均通过；它不代替从Finder启动的GUI权限归属验收。Windows只读探针确认两屏仍为(-1080,-493,1080×1920)及(0,0,2560×1440)。任务123的诊断命令继承临时测试profile，输出角色不是用户实际GUI角色；启动脚本另外验证复用实际agent配置。
- 实机运行记录：两端各一GUI及受管service。Mac日志22:02以后多次“跨屏控制已启动”及恢复边缘监视，没有启动错误。用户明确确认新版鼠标跨屏体验正常。Windows托盘外观和实际重复打开后的窗口行为，用户尚未单独回报；相应后台机制通过。未据此声称完成全部键盘/多屏/异常场景人工矩阵，也未测量端到端延迟。

## 交付

| 平台 | ZIP SHA-256 |
|---|---|
| Mac ARM64 | 0717d0cdac1435fefcd1a55d21c6e8197f4aafb8f003743222bbca78d2500485 |
| Windows x64 | d10025aa832462298edcbbb549f3191d2320a05f2b5c581b345c34338e24441a |

Mac应用和ZIP在work/single-instance-validation。Windows ZIP经SHA-256验证后解压至E:\test\MyApp-0.1.9-alpha-instance\app，使用Launch-in-E-test.ps1；优先复用0.1.8、0.1.7、0.1.6已有profile，保留原信任与用户位置。所有Windows软件、临时数据和检查仍在E:\test，没有修改网络/防火墙设置。本轮预览包不是正式GitHub Release。

Windows命名对象依据：[CreateMutexW](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createmutexw)、[CreateEventW](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createeventw)。当前实现约束当前登录桌面环境，未声称跨所有系统用户/远程桌面会话的机器级独占。

收尾时临时Windows客户端确认退出，Mac传送端和本轮开发容器已关闭；未停止用户手动打开的MyApp程序。
