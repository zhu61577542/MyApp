//go:build gui

package nativegui

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"os"
	"path/filepath"
	"time"

	"myapp/internal/logging"
)

func exportLogs() (string, error) {
	destination, err := logExportPath()
	if err != nil {
		return "", err
	}
	if err := logging.Export(logging.DefaultPath(), destination); err != nil {
		return "", err
	}
	return destination, nil
}

func logExportPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(home, "Downloads")
	if _, err := os.Stat(directory); err != nil {
		directory = filepath.Dir(logging.DefaultPath())
	}
	destination := filepath.Join(directory, fmt.Sprintf("MyApp-logs-%s.log", time.Now().Format("20060102-150405")))
	return destination, nil
}

func (v *desktopView) exportLogDialog(report func(string)) {
	destination, err := logExportPath()
	if err != nil {
		report("无法选择导出位置：" + err.Error())
		return
	}
	owner := v.window
	if v.settingsWindow != nil {
		owner = v.settingsWindow
	}
	dialog.ShowConfirm("导出诊断日志？", "将复制日志到：\n"+destination+"\n原日志会保留。", func(ok bool) {
		if !ok {
			return
		}
		report("正在导出日志…")
		go func() {
			err := logging.Export(logging.DefaultPath(), destination)
			fyne.Do(func() {
				if err != nil {
					report("导出失败：" + err.Error())
				} else {
					report("日志已导出：" + destination)
				}
			})
		}()
	}, owner)
}
