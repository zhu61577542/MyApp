//go:build gui

package nativegui

import (
	"strings"
	"testing"
)

func TestPairingFailureMessageExplainsConnectionRefused(t *testing.T) {
	message := pairingOutputMessage("错误: dial tcp 172.27.0.223:24800: connect: connection refused")
	if !strings.Contains(message, "没有开启配对监听") {
		t.Fatalf("未生成可操作提示: %s", message)
	}
	if strings.Contains(message, "exit status 1") {
		t.Fatalf("不应直接显示进程退出码: %s", message)
	}
	if message := pairingFailureMessage("dial tcp: connect: connection refused"); !strings.Contains(message, "未开启监听") {
		t.Fatalf("失败提示不完整: %s", message)
	}
}
