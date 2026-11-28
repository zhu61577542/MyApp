//go:build windows

package pointer

import (
	"errors"
	"testing"

	common "myapp/internal/input"
)

func TestWindowsCursorDPIContextMatchesGeometryAndRestores(t *testing.T) {
	for _, point := range []Point{{2559, 500}, {-1080, -400}, {100, 1439}} {
		t.Run(point.KeyForTest(), func(t *testing.T) {
			original := ^uintptr(0)
			current := original
			b := &nativeBackend{}
			b.cursorContext = func(next uintptr) (uintptr, error) { old := current; current = next; return old, nil }
			b.cursorRead = func(out *nativePoint) error {
				if current != ^uintptr(3) {
					out.X = int32(point.X * 4 / 5)
					out.Y = int32(point.Y * 4 / 5)
				} else {
					out.X = int32(point.X)
					out.Y = int32(point.Y)
				}
				return nil
			}
			b.cursorWarp = func(got Point) error {
				if current != ^uintptr(3) || got != point {
					t.Fatal("恢复光标使用了缩放坐标")
				}
				return nil
			}
			got, err := b.Position()
			if err != nil || got != point || current != original {
				t.Fatal("位置与物理几何不一致，或泄漏DPI上下文")
			}
			if err := b.Warp(point); err != nil || current != original {
				t.Fatal("定位未恢复原DPI上下文")
			}
			if !common.ConsumeCursorWarp(int32(point.X), int32(point.Y)) {
				t.Fatal("定位丢失防反弹标记")
			}
		})
	}
}

func (p Point) KeyForTest() string {
	if p.X < 0 {
		return "负坐标扩展屏"
	}
	if p.X > 100 {
		return "右边缘"
	}
	return "下边缘"
}

func TestWindowsCursorDPIContextFailuresAreVisible(t *testing.T) {
	expected := errors.New("DPI上下文不可用")
	b := &nativeBackend{cursorContext: func(uintptr) (uintptr, error) { return 0, expected }, cursorRead: func(*nativePoint) error { t.Fatal("上下文失败仍读光标"); return nil }, cursorWarp: func(Point) error { t.Fatal("上下文失败仍定位"); return nil }}
	if _, err := b.Position(); !errors.Is(err, expected) {
		t.Fatal("未报告DPI错误")
	}
	if err := b.Warp(Point{}); !errors.Is(err, expected) {
		t.Fatal("未报告DPI错误")
	}
	for _, restoreFailure := range []bool{false, true} {
		calls := 0
		b.cursorContext = func(uintptr) (uintptr, error) {
			calls++
			if restoreFailure && calls == 2 {
				return 0, expected
			}
			return ^uintptr(0), nil
		}
		b.cursorRead = func(*nativePoint) error { return expected }
		if _, err := b.Position(); !errors.Is(err, expected) || calls != 2 {
			t.Fatal("读取失败未恢复上下文")
		}
		calls = 0
		b.cursorWarp = func(Point) error { return expected }
		if err := b.Warp(Point{1, 2}); !errors.Is(err, expected) || calls != 2 {
			t.Fatal("定位失败未恢复上下文")
		}
		if common.ConsumeCursorWarp(1, 2) {
			t.Fatal("失败定位遗留防反弹标记")
		}
	}
}
