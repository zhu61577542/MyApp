//go:build darwin

package pointer

import (
	"os"
	"testing"
)

func TestMacNativeTopologyReadOnly(t *testing.T) {
	if os.Getenv("MYAPP_MAC_READONLY_POINTER_TEST") != "1" {
		t.Skip("需显式启用只读显示器检查")
	}
	backend, err := New()
	if err != nil {
		t.Fatal(err)
	}
	topology, err := TopologyFor(backend)
	if err != nil || !topology.Valid() {
		t.Fatal("无法读取有效的当前显示器布局", err)
	}
	source, err := SourceScreen(backend)
	if err != nil || source != topology.Bounds {
		t.Fatal("来源边界未跟随运行时拓扑", err)
	}
	if _, err := backend.Position(); err != nil {
		t.Fatal(err)
	}
	t.Logf("只读查询通过：%d台有效显示器，桌面范围%dx%d；未安装Hook或操作光标", len(topology.Displays), source.Width, source.Height)
}
