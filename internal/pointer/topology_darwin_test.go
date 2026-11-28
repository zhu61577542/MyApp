//go:build darwin

package pointer

import (
	"testing"
	"unsafe"
)

func macTopologyFixture(ids []uint32, bounds map[uint32]cgRect) *nativeBackend {
	return &nativeBackend{
		activeDisplays: func(capacity uint32, list *uint32, count *uint32) int32 {
			*count = uint32(len(ids))
			if list != nil {
				copy(unsafe.Slice(list, int(capacity)), ids)
			}
			return 0
		},
		mirrorsDisplay: func(uint32) uint32 { return 0 },
		bounds:         func(id uint32) cgRect { return bounds[id] },
	}
}

func TestMacRuntimeTopologyUsesGlobalCoordinatesAndFiltersMirrors(t *testing.T) {
	b := macTopologyFixture([]uint32{1, 2, 3}, map[uint32]cgRect{
		1: {Size: cgSize{1512, 982}},
		2: {Origin: cgPoint{-1080, -300}, Size: cgSize{1080, 1920}},
		3: {Size: cgSize{1512, 982}},
	})
	b.mirrorsDisplay = func(id uint32) uint32 {
		if id == 3 {
			return 1
		}
		return 0
	}
	topology, err := b.Topology()
	if err != nil || len(topology.Displays) != 2 || topology.Bounds.X != -1080 || topology.Bounds.Y != -300 || topology.Bounds.Width != 2592 || topology.Bounds.Height != 1920 {
		t.Fatal("未使用实时全局坐标或未过滤镜像", topology, err)
	}
	if !topology.Valid() {
		t.Fatal("生成无效布局")
	}
	before := topology.Bounds.Key()
	b.bounds = func(id uint32) cgRect {
		if id == 2 {
			return cgRect{Origin: cgPoint{-1080, -300}, Size: cgSize{1080, 1920}}
		}
		return cgRect{Size: cgSize{1728, 1117}}
	}
	changed, err := b.Topology()
	if err != nil || changed.Bounds.Key() == before || changed.Bounds.Width != 2808 {
		t.Fatal("运行时更换缩放/分辨率后仍使用旧布局")
	}
}

func TestMacRuntimeTopologyRejectsUnstableOrInvalidDisplays(t *testing.T) {
	for _, count := range []int{0, 33} {
		b := macTopologyFixture(make([]uint32, count), nil)
		if _, err := b.Topology(); err == nil {
			t.Fatal("不应接受显示器数量", count)
		}
	}
	b := macTopologyFixture([]uint32{1}, map[uint32]cgRect{1: {}})
	if _, err := b.Topology(); err == nil {
		t.Fatal("显示器失效仍返回旧布局")
	}
	calls := 0
	b.activeDisplays = func(capacity uint32, list *uint32, count *uint32) int32 {
		calls++
		if list == nil {
			*count = 1
		} else {
			*count = 0
		}
		return 0
	}
	if _, err := b.Topology(); err == nil || calls != 6 {
		t.Fatal("反复热变更仍公布部分布局", calls)
	}
	b.activeDisplays = func(uint32, *uint32, *uint32) int32 { return -1 }
	if _, err := b.Topology(); err == nil {
		t.Fatal("枚举失败未报告")
	}
}
