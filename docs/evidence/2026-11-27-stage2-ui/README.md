# 第二阶段原生界面预览交付

用户要求实际运行带UI程序，不能仅用后台测试验收。已从源码60543e6b构建0.1.10-alpha-stage2的Mac ARM64和Windows x64原生GUI；未使用gui/ci标签替代实际图形驱动构建。

Mac包：work/peer-stage2-ui/MyApp.app。沿用local.myapp.client标识与权限用途说明，CFBundleShortVersionString为0.1.10。已完成adhoc签名和严格签名检查，CLI版本输出为0.1.10-alpha-stage2、commit=60543e6b。

Windows包已校验ZIP与EXE的SHA-256，送至E:\test\MyApp-0.1.10-alpha-stage2\app。PE子系统为Windows GUI（2），x64，导入DLL均为系统库；启动脚本AST检查及实际version命令通过。启动脚本沿用原测试profile、身份与信任，所有新增文件仍在E:\test。回执见131-windows-gui-delivery.json。

构建全部使用Docker内Go1.26.1。Mac沿用已有15.2 SDK和Zig，添加系统头文件、框架及库搜索路径后成功；首轮和头文件参数尝试记录单独保留。Windows使用已有fyne-cross镜像中的MinGW，临时构建容器已删除，生成的临时syso资源已清理。

两端旧0.1.9仍在运行，尚未由助手启动新GUI。请用户先从旧程序菜单栏/托盘退出，再启动本预览版。仅在用户报告实际界面结果后标记UI项目通过。界面沿用既有角色设计；阶段3平台输入过滤和阶段4完整界面迁移仍未完成。

日志起点和记录范围见session.json；只记录用户反馈、启动、连接、权限和错误，不进行原始键盘记录、剪贴板内容采集或屏幕录制。
