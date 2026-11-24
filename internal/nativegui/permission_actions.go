//go:build gui

package nativegui

import (
	"context"
	"runtime"
	"time"

	"fyne.io/fyne/v2/dialog"
)

func (v *desktopView) requestPermissions(automatic bool) {
	if runtime.GOOS != "darwin" || !v.ready || (automatic && v.prefs.PermissionRequestVersion >= 3) {
		return
	}
	v.prefs.PermissionRequestVersion = 3
	if err := savePreferences(v.options, v.prefs); err != nil {
		v.showError(err)
		return
	}
	if err := requestPlatformPermissions(v.cfg.Role); err != nil {
		v.showError(err)
		return
	}
	v.permissionTime = time.Time{}
	v.wantConnect = true
	v.notice.SetText("已向系统请求 MyApp 授权，请在系统提示中确认；无需手动选择应用文件。已拒绝的项目可通过权限行打开设置重新允许。")
	v.refresh(true)
}

func (v *desktopView) resetPermissions() {
	dialog.ShowConfirm("删除 MyApp 的权限？", "共享与配对会先停止，然后重置 MyApp 的键鼠授权。不会修改其他软件的权限。macOS 当前不支持由程序清除本地网络权限，该项将保留。", func(ok bool) {
		if !ok {
			return
		}
		v.perform("正在停止共享并重置 MyApp 权限…", func(ctx context.Context) error {
			if err := v.pairings.stopAndWait(ctx); err != nil {
				return err
			}
			if err := v.services.stopGracefully(ctx); err != nil {
				return err
			}
			return resetPlatformPermissions(ctx)
		}, func() {
			v.wantConnect = false
			v.autoAttempted = true
			v.permissionTime = time.Time{}
			dialog.ShowInformation("MyApp 键鼠权限已重置", "输入监控及控制键鼠的 TCC 授权已重置。系统的本地网络登记不受此命令管理，未被清除。再次使用时点击“自动请求授权”，并按系统提示确认。", v.window)
		})
	}, v.window)
}
