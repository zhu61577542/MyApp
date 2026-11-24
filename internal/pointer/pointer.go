package pointer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"myapp/internal/layout"
	"time"
)

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}
type Screen struct {
	ID     string `json:"id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (s Screen) Valid() bool {
	return s.ID != "" && len(s.ID) <= 64 && s.Width >= 8 && s.Height >= 8 && s.Width <= 1000000 && s.Height <= 1000000 && s.X >= -1000000 && s.Y >= -1000000 && int64(s.X)+int64(s.Width) <= 1000000 && int64(s.Y)+int64(s.Height) <= 1000000
}
func (s Screen) Key() string { return fmt.Sprintf("%s:%d:%d:%d:%d", s.ID, s.X, s.Y, s.Width, s.Height) }
func (s Screen) Contains(p Point) bool {
	return p.X >= s.X && p.Y >= s.Y && p.X < s.X+s.Width && p.Y < s.Y+s.Height
}

type Backend interface {
	Screen() (Screen, error)
	Position() (Point, error)
	Warp(Point) error
	Hide(context.Context) (func() error, error)
}

type CursorState struct {
	Visible             bool `json:"visible"`
	TransparentStandard int  `json:"transparent_standard"`
	CheckedStandard     int  `json:"checked_standard"`
}
type Inspector interface{ InspectCursor() (CursorState, error) }

type Entry struct {
	Side      string  `json:"side"`
	Position  float64 `json:"position"`
	ScreenKey string  `json:"screen_key"`
}

func (e Entry) Valid() bool {
	return (e.Side == "left" || e.Side == "right" || e.Side == "up" || e.Side == "down") && !math.IsNaN(e.Position) && !math.IsInf(e.Position, 0) && e.Position >= 0 && e.Position <= 1 && len(e.ScreenKey) > 0 && len(e.ScreenKey) <= 160
}

func Landing(screen Screen, entry Entry, returning bool) (Point, error) {
	if !screen.Valid() || !entry.Valid() {
		return Point{}, errors.New("跨屏几何或入口无效")
	}
	point := Point{screen.X + int(math.Round(entry.Position*float64(screen.Width-1))), screen.Y + int(math.Round(entry.Position*float64(screen.Height-1)))}
	side := entry.Side
	if returning {
		side = map[string]string{"left": "right", "right": "left", "up": "down", "down": "up"}[side]
	}
	switch side {
	case "right":
		point.X = screen.X + 2
	case "left":
		point.X = screen.X + screen.Width - 3
	case "down":
		point.Y = screen.Y + 2
	case "up":
		point.Y = screen.Y + screen.Height - 3
	}
	return point, nil
}

type Travel struct {
	displays []Screen
	Screen   Screen
	Entry    Entry
	Point    Point
}

func NewTravel(screen Screen, entry Entry) (*Travel, error) {
	point, err := Landing(screen, entry, false)
	if err != nil {
		return nil, err
	}
	return &Travel{Screen: screen, Entry: entry, Point: point}, nil
}
func (t *Travel) Move(dx, dy int) (Point, bool) {
	x, y := int64(t.Point.X)+int64(dx), int64(t.Point.Y)+int64(dy)
	back := t.Entry.Side == "right" && x < int64(t.Screen.X) || t.Entry.Side == "left" && x >= int64(t.Screen.X+t.Screen.Width) || t.Entry.Side == "down" && y < int64(t.Screen.Y) || t.Entry.Side == "up" && y >= int64(t.Screen.Y+t.Screen.Height)
	t.Point = Point{int(max(int64(t.Screen.X), min(x, int64(t.Screen.X+t.Screen.Width-1)))), int(max(int64(t.Screen.Y), min(y, int64(t.Screen.Y+t.Screen.Height-1))))}
	if len(t.displays) > 0 {
		candidate := t.Point
		left, right, top, bottom := int64(1000001), int64(-1000001), int64(1000001), int64(-1000001)
		for _, s := range t.displays {
			if candidate.Y >= s.Y && candidate.Y < s.Y+s.Height {
				left = min(left, int64(s.X))
				right = max(right, int64(s.X+s.Width))
			}
			if candidate.X >= s.X && candidate.X < s.X+s.Width {
				top = min(top, int64(s.Y))
				bottom = max(bottom, int64(s.Y+s.Height))
			}
		}
		back = t.Entry.Side == "right" && dx < 0 && left <= 1000000 && x < left || t.Entry.Side == "left" && dx > 0 && right >= -1000000 && x >= right || t.Entry.Side == "down" && dy < 0 && top <= 1000000 && y < top || t.Entry.Side == "up" && dy > 0 && bottom >= -1000000 && y >= bottom
		t.Point = project(candidate, t.displays)
	}
	return t.Point, back
}
func (t *Travel) ReturnRatio() float64 {
	if t.Entry.Side == "left" || t.Entry.Side == "right" {
		return float64(t.Point.Y-t.Screen.Y) / float64(t.Screen.Height-1)
	}
	return float64(t.Point.X-t.Screen.X) / float64(t.Screen.Width-1)
}

type Watcher struct {
	armed  bool
	screen string
	last   layout.Switch
	since  time.Time
}

func (w *Watcher) Reset() { w.armed = false; w.last = layout.Switch{}; w.since = time.Time{} }
func (w *Watcher) Observe(point Point, screen Screen, neighbors map[string]string, delay time.Duration, now time.Time) (string, Entry, bool) {
	if !screen.Valid() || !screen.Contains(point) {
		w.Reset()
		return "", Entry{}, false
	}
	if w.screen != screen.Key() {
		w.Reset()
		w.screen = screen.Key()
	}
	x, y := point.X-screen.X, point.Y-screen.Y
	if x >= 6 && y >= 6 && x < screen.Width-6 && y < screen.Height-6 {
		w.armed = true
		w.last = layout.Switch{}
		w.since = time.Time{}
		return "", Entry{}, false
	}
	if !w.armed {
		return "", Entry{}, false
	}
	board, _ := layout.New(2)
	for side, id := range neighbors {
		mapped := layout.Side(side)
		if side == "up" {
			mapped = layout.Top
		}
		if side == "down" {
			mapped = layout.Bottom
		}
		if board.Set(mapped, id) != nil {
			return "", Entry{}, false
		}
	}
	hit, ok := board.Hit(layout.Point{X: x, Y: y}, layout.Display{Width: screen.Width, Height: screen.Height}, 2)
	if !ok {
		w.last = layout.Switch{}
		w.since = time.Time{}
		return "", Entry{}, false
	}
	if w.last.DeviceID != hit.DeviceID || w.last.Side != hit.Side {
		w.last = hit
		w.since = now
	}
	if now.Sub(w.since) < delay {
		return "", Entry{}, false
	}
	w.armed = false
	side := string(hit.Side)
	if hit.Side == layout.Top {
		side = "up"
	}
	if hit.Side == layout.Bottom {
		side = "down"
	}
	return hit.DeviceID, Entry{Side: side, Position: hit.Position, ScreenKey: screen.Key()}, true
}
