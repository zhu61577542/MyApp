//go:build darwin

package pointer

import (
	"errors"
	"fmt"
)

func (p *nativeBackend) Topology() (Topology, error) {
	for range 2 {
		var count uint32
		if code := p.activeDisplays(0, nil, &count); code != 0 {
			return Topology{}, fmt.Errorf("读取 Mac 显示器数量失败: %d", code)
		}
		if count == 0 || count > 32 {
			return Topology{}, errors.New("Mac 活动显示器数量无效，支持1至32台")
		}
		ids := make([]uint32, count)
		var returned uint32
		if code := p.activeDisplays(count, &ids[0], &returned); code != 0 {
			return Topology{}, fmt.Errorf("枚举 Mac 显示器失败: %d", code)
		}
		var checked uint32
		if code := p.activeDisplays(0, nil, &checked); code != 0 {
			return Topology{}, fmt.Errorf("复查 Mac 显示器数量失败: %d", code)
		}
		if returned != count || checked != count {
			continue
		}
		displays := make([]Screen, 0, count)
		for _, id := range ids {
			if p.mirrorsDisplay(id) != 0 {
				continue
			}
			bounds := p.bounds(id)
			displays = append(displays, Screen{ID: fmt.Sprintf("%x", id), X: int(bounds.Origin.X), Y: int(bounds.Origin.Y), Width: int(bounds.Size.Width), Height: int(bounds.Size.Height)})
		}
		return NewTopology(displays)
	}
	return Topology{}, errors.New("Mac 显示器正在变化，请稍后重试")
}
