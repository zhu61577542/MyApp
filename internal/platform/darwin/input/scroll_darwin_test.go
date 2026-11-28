//go:build darwin

package darwininput

import (
	"os"
	"testing"

	"github.com/ebitengine/purego"
	common "myapp/internal/input"
)

func TestMacNativeScrollUnitsWithoutPosting(t *testing.T) {
	if os.Getenv("MYAPP_MAC_READONLY_SCROLL_EVENT_TEST") != "1" {
		t.Skip("需显式启用不投递的滚轮工厂检查")
	}
	i, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var integer func(uintptr, uint32) int64
	purego.RegisterLibFunc(&integer, cg, "CGEventGetIntegerValueField")
	for _, unit := range []uint32{0, 1} {
		event := i.createScroll(0, unit, 2, -3, 2, 0)
		if event == 0 {
			t.Fatal("原生滚轮工厂返回空事件")
		}
		continuous, vertical, horizontal := integer(event, 88), integer(event, 11), integer(event, 12)
		i.release(event)
		if unit == 1 && (continuous != 0 || vertical != -3 || horizontal != 2) {
			t.Fatal("行单位事件数据错误", continuous, vertical, horizontal)
		}
		if unit == 0 && continuous == 0 {
			t.Fatal("像素事件没有标记为连续滚动")
		}
		t.Logf("仅创建不投递：unit=%d continuous=%d vertical=%d horizontal=%d", unit, continuous, vertical, horizontal)
	}
}

func TestMacScrollUsesLineUnitsAndPreservesBothAxes(t *testing.T) {
	created, marked, posted := false, false, false
	i := &Injector{
		tracker: common.NewTracker(),
		createScroll: func(source uintptr, units, count uint32, vertical, horizontal, third int32) uintptr {
			if source != 0 || units != 1 || count != 2 || vertical != -3 || horizontal != 2 || third != 0 {
				t.Fatal("滚轮单位或轴错误", units, count, vertical, horizontal)
			}
			created = true
			return 1
		},
		setInteger: func(_ uintptr, field uint32, value int64) {
			if field != eventSourceUserData || value != myAppEventMarker {
				t.Fatal("滚动缺少注入标记")
			}
			marked = true
		},
		post: func(uint32, uintptr) {
			if !created || !marked {
				t.Fatal("注入顺序错误")
			}
			posted = true
		},
		release: func(uintptr) {},
	}
	if err := i.Inject(common.Event{Kind: common.MouseScroll, Sequence: 1, X: 2, Y: -3}); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("未转交滚动事件")
	}
}
