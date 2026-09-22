package layout

import (
	"errors"
	"math"
	"sync"
)

type Side string

const (
	Left   Side = "left"
	Right  Side = "right"
	Top    Side = "top"
	Bottom Side = "bottom"
)

type Point struct {
	X int
	Y int
}

type Display struct {
	Width  int
	Height int
}

type Switch struct {
	DeviceID string
	Side     Side
	Position float64
}

type Layout struct {
	mu        sync.RWMutex
	neighbors map[Side]string
	maxPeers  int
}

func New(maxPeers int) (*Layout, error) {
	if maxPeers < 1 || maxPeers > 2 {
		return nil, errors.New("第一版相邻设备数必须在 1 到 2 之间")
	}
	return &Layout{neighbors: make(map[Side]string), maxPeers: maxPeers}, nil
}

func (l *Layout) Set(side Side, deviceID string) error {
	if !validSide(side) || deviceID == "" {
		return errors.New("屏幕方向或设备 ID 无效")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for existingSide, existingID := range l.neighbors {
		if existingID == deviceID && existingSide != side {
			return errors.New("同一设备不能占用多个方向")
		}
	}
	if _, exists := l.neighbors[side]; !exists && len(l.neighbors) >= l.maxPeers {
		return errors.New("相邻设备数量已达到上限")
	}
	l.neighbors[side] = deviceID
	return nil
}

func (l *Layout) Remove(side Side) {
	l.mu.Lock()
	delete(l.neighbors, side)
	l.mu.Unlock()
}

func (l *Layout) Hit(point Point, display Display, threshold int) (Switch, bool) {
	if display.Width < 2 || display.Height < 2 || threshold < 1 {
		return Switch{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	type candidate struct {
		side     Side
		distance int
		position float64
		deviceID string
	}
	var candidates []candidate
	if id := l.neighbors[Left]; id != "" && point.X >= 0 && point.X < threshold {
		candidates = append(candidates, candidate{Left, point.X, normalized(point.Y, display.Height), id})
	}
	if id := l.neighbors[Right]; id != "" && point.X < display.Width && display.Width-1-point.X < threshold {
		candidates = append(candidates, candidate{Right, display.Width - 1 - point.X, normalized(point.Y, display.Height), id})
	}
	if id := l.neighbors[Top]; id != "" && point.Y >= 0 && point.Y < threshold {
		candidates = append(candidates, candidate{Top, point.Y, normalized(point.X, display.Width), id})
	}
	if id := l.neighbors[Bottom]; id != "" && point.Y < display.Height && display.Height-1-point.Y < threshold {
		candidates = append(candidates, candidate{Bottom, display.Height - 1 - point.Y, normalized(point.X, display.Width), id})
	}
	if len(candidates) == 0 {
		return Switch{}, false
	}
	best := candidates[0]
	for _, current := range candidates[1:] {
		if current.distance < best.distance {
			best = current
		}
	}
	return Switch{DeviceID: best.deviceID, Side: best.side, Position: best.position}, true
}

func Landing(side Side, position float64, remote Display) (Point, error) {
	if !validSide(side) || remote.Width < 2 || remote.Height < 2 || math.IsNaN(position) {
		return Point{}, errors.New("远端落点参数无效")
	}
	position = math.Max(0, math.Min(1, position))
	switch side {
	case Left:
		return Point{X: remote.Width - 2, Y: int(math.Round(position * float64(remote.Height-1)))}, nil
	case Right:
		return Point{X: 1, Y: int(math.Round(position * float64(remote.Height-1)))}, nil
	case Top:
		return Point{X: int(math.Round(position * float64(remote.Width-1))), Y: remote.Height - 2}, nil
	case Bottom:
		return Point{X: int(math.Round(position * float64(remote.Width-1))), Y: 1}, nil
	default:
		return Point{}, errors.New("屏幕方向无效")
	}
}

func normalized(value, size int) float64 {
	if value <= 0 {
		return 0
	}
	if value >= size-1 {
		return 1
	}
	return float64(value) / float64(size-1)
}

func validSide(side Side) bool {
	return side == Left || side == Right || side == Top || side == Bottom
}
