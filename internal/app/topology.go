package app

import (
	"errors"
	"myapp/internal/pointer"
	"myapp/internal/protocol"
)

func (l *Link) EnableTopology(local func() (pointer.Topology, error)) {
	if l.screen != nil {
		l.screen.localTopology = local
	}
}
func (l *Link) handleTopology(frame protocol.Frame) error {
	if l.screen == nil {
		return nil
	}
	if len(frame.Payload) > 16384 {
		return errors.New("显示器布局消息过长")
	}
	var message struct {
		Version  int              `json:"version"`
		Topology pointer.Topology `json:"topology"`
	}
	if err := decodeStrictJSON(frame.Payload, &message); err != nil {
		return err
	}
	t := message.Topology
	if message.Version != 1 || (!t.Valid() && !(len(t.Displays) == 0 && t.Bounds == (pointer.Screen{}))) {
		return errors.New("显示器布局消息无效")
	}
	old := l.RemoteScreen()
	l.screen.mu.Lock()
	l.screen.remoteTopology = &t
	l.screen.mu.Unlock()
	info := l.RemoteScreen()
	if l.screen.onInfo != nil {
		l.screen.onInfo(info)
	}
	if old.Version != 0 && old.Screen.Key() != info.Screen.Key() && l.screen.onChange != nil {
		l.screen.onChange()
	}
	return nil
}
