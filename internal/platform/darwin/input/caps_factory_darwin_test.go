//go:build darwin

package darwininput

import (
	"os"
	"testing"

	"github.com/ebitengine/purego"
)

func TestMacNativeCapsEventFactoryWithoutPosting(t *testing.T) {
	if os.Getenv("MYAPP_MAC_READONLY_KEY_EVENT_TEST") != "1" {
		t.Skip("需显式启用不投递事件的原生工厂检查")
	}
	i, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var getType func(uintptr) uint32
	var getFlags func(uintptr) uint64
	purego.RegisterLibFunc(&getType, cg, "CGEventGetType")
	purego.RegisterLibFunc(&getFlags, cg, "CGEventGetFlags")
	for _, down := range []bool{true, false} {
		event := i.createKeyboard(0, 57, down)
		if event == 0 {
			t.Fatal("原生事件创建失败")
		}
		t.Logf("未投递的Caps事件：down=%t type=%d alphaShift=%t", down, getType(event), getFlags(event)&(1<<16) != 0)
		i.release(event)
	}
}
