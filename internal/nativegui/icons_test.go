//go:build gui

package nativegui

import (
	"bytes"
	"image"
	_ "image/png"
	"testing"

	"github.com/fyne-io/image/ico"
)

func TestWindowsTrayResourceConvertsToNativeIcon(t *testing.T) {
	img, format, err := image.Decode(bytes.NewReader(systemTrayIcon("windows").Content()))
	if err != nil || format != "png" {
		t.Fatalf("Windows 托盘图标不能解码: %s %v", format, err)
	}
	if err := ico.Encode(&bytes.Buffer{}, img); err != nil {
		t.Fatalf("Windows ICO 转换失败: %v", err)
	}
}
