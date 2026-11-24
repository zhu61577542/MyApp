package app

import (
	"context"
	"encoding/json"
	"errors"
	"myapp/internal/pointer"
	"myapp/internal/protocol"
	"myapp/internal/transport"
	"sync"
	"time"
)

type ScreenInfo struct {
	Topology *pointer.Topology `json:"-"`
	Version  int               `json:"version"`
	Screen   pointer.Screen    `json:"screen"`
}
type linkScreen struct {
	localTopology  func() (pointer.Topology, error)
	remoteTopology *pointer.Topology
	mu             sync.Mutex
	local          func() (pointer.Screen, error)
	remote         ScreenInfo
	onInfo         func(ScreenInfo)
	onChange       func()
}

func (l *Link) EnableScreen(local func() (pointer.Screen, error), receive func(ScreenInfo)) {
	l.screen = &linkScreen{local: local, onInfo: receive}
}
func (l *Link) SetScreenChangeHandler(handler func()) {
	if l.screen != nil {
		l.screen.onChange = handler
	}
}
func (l *Link) RemoteScreen() ScreenInfo {
	if l.screen == nil {
		return ScreenInfo{}
	}
	l.screen.mu.Lock()
	defer l.screen.mu.Unlock()
	if t := l.screen.remoteTopology; t != nil {
		return ScreenInfo{Version: 1, Screen: t.Bounds, Topology: t}
	}
	return l.screen.remote
}

func (l *Link) publishScreens(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := ""
	lastTopology := ""
	for {
		info := ScreenInfo{Version: 1}
		if l.screen.local != nil {
			if screen, err := l.screen.local(); err == nil && screen.Valid() {
				info.Screen = screen
			}
		}
		if l.screen.localTopology != nil {
			topology, err := l.screen.localTopology()
			if err != nil || !topology.Valid() {
				topology = pointer.Topology{}
			}
			key := topology.Bounds.Key()
			if key != lastTopology {
				payload, _ := json.Marshal(struct {
					Version  int              `json:"version"`
					Topology pointer.Topology `json:"topology"`
				}{1, topology})
				writer := l.bulkWriter
				if writer == nil {
					writer = l.writer
				}
				if writer.Enqueue(ctx, transport.PriorityBulk, protocol.Frame{Type: protocol.TypeScreenTopology, Payload: payload}) != nil {
					return
				}
				lastTopology = key
			}
		}
		key := info.Screen.Key()
		if key != last {
			payload, _ := json.Marshal(info)
			writer := l.bulkWriter
			if writer == nil {
				writer = l.writer
			}
			if writer.Enqueue(ctx, transport.PriorityBulk, protocol.Frame{Type: protocol.TypeScreenInfo, Payload: payload}) != nil {
				return
			}
			last = key
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (l *Link) handleScreen(frame protocol.Frame) error {
	if l.screen == nil {
		return nil
	}
	if len(frame.Payload) > 4096 {
		return errors.New("屏幕能力消息过长")
	}
	var info ScreenInfo
	if err := decodeStrictJSON(frame.Payload, &info); err != nil {
		return err
	}
	if info.Version != 1 || info.Screen.ID != "" && !info.Screen.Valid() {
		return errors.New("屏幕能力无效")
	}
	l.screen.mu.Lock()
	old := l.screen.remote
	l.screen.remote = info
	l.screen.mu.Unlock()
	if l.screen.onInfo != nil {
		l.screen.onInfo(l.RemoteScreen())
	}
	if old.Version != 0 && old.Screen.Key() != info.Screen.Key() && l.screen.onChange != nil {
		l.screen.onChange()
	}
	return nil
}
