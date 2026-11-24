package pointer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

type Topology struct {
	Bounds   Screen   `json:"bounds"`
	Displays []Screen `json:"displays"`
}
type TopologyBackend interface{ Topology() (Topology, error) }

func NewTopology(displays []Screen) (Topology, error) {
	if len(displays) == 0 || len(displays) > 32 {
		return Topology{}, errors.New("显示器数量无效")
	}
	displays = append([]Screen(nil), displays...)
	sort.Slice(displays, func(i, j int) bool { return displays[i].Key() < displays[j].Key() })
	x, y, r, b := displays[0].X, displays[0].Y, displays[0].X+displays[0].Width, displays[0].Y+displays[0].Height
	keys := ""
	seen := map[string]bool{}
	for _, s := range displays {
		if !s.Valid() || seen[s.ID] {
			return Topology{}, errors.New("显示器几何或标识无效")
		}
		seen[s.ID] = true
		keys += s.Key() + ";"
		x = min(x, s.X)
		y = min(y, s.Y)
		r = max(r, s.X+s.Width)
		b = max(b, s.Y+s.Height)
	}
	hash := sha256.Sum256([]byte(keys))
	bounds := Screen{ID: fmt.Sprintf("desktop-%x", hash[:12]), X: x, Y: y, Width: r - x, Height: b - y}
	if !bounds.Valid() {
		return Topology{}, errors.New("桌面范围无效")
	}
	return Topology{Bounds: bounds, Displays: displays}, nil
}
func (t Topology) Valid() bool {
	expected, err := NewTopology(t.Displays)
	return err == nil && expected.Bounds == t.Bounds
}
func TopologyFor(backend Backend) (Topology, error) {
	if t, ok := backend.(TopologyBackend); ok {
		return t.Topology()
	}
	s, e := backend.Screen()
	if e != nil {
		return Topology{}, e
	}
	return NewTopology([]Screen{s})
}
func ScreenWithKey(backend Backend, key string) (Screen, error) {
	s, e := backend.Screen()
	if e == nil && s.Key() == key {
		return s, nil
	}
	t, e := TopologyFor(backend)
	if e == nil && t.Bounds.Key() == key {
		return t.Bounds, nil
	}
	return Screen{}, errors.New("显示器布局已经变化")
}
func project(point Point, displays []Screen) Point {
	best := point
	distance := int64(1 << 62)
	for _, s := range displays {
		p := Point{max(s.X, min(point.X, s.X+s.Width-1)), max(s.Y, min(point.Y, s.Y+s.Height-1))}
		dx, dy := int64(point.X-p.X), int64(point.Y-p.Y)
		d := dx*dx + dy*dy
		if d < distance {
			best = p
			distance = d
		}
	}
	return best
}
func NewTopologyTravel(t Topology, entry Entry) (*Travel, error) {
	if !t.Valid() {
		return nil, errors.New("显示器布局无效")
	}
	travel, err := NewTravel(t.Bounds, entry)
	if err != nil {
		return nil, err
	}
	travel.displays = t.Displays
	travel.Point = project(travel.Point, t.Displays)
	return travel, nil
}
