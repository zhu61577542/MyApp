# MyApp 图标资源

- `icon.svg`：原始品牌参考图，不直接用于平台打包。
- `myapp-app.svg`：应用图标源图，蓝色圆角底、白色鼠标指针，适合窗口和启动器。
- `myapp-tray.svg`：深色托盘图标，适合浅色菜单栏或任务栏。
- `myapp-tray-template.svg`：黑色模板图标，macOS 可按系统菜单栏自动反色。

平台使用约定：

| 平台 | 应用图标 | 托盘图标 |
|---|---|---|
| macOS | `myapp-app.svg` 转 `.icns` | `myapp-tray-template.svg` 的单色模板资源 |
| Windows | `myapp-app.svg` 转多尺寸 `.ico` | `myapp-tray.svg` 转 16/24/32 像素 PNG |
| Linux | `myapp-app.svg` 转 128/256/512 像素 PNG | `myapp-tray.svg` 转 16/22/24/32 像素 PNG |

源 SVG 不依赖 `currentColor`，因此脱离浏览器后颜色稳定；小尺寸版本使用透明背景和固定线宽，避免托盘图标在深浅色桌面上丢失。
