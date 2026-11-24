package main

import (
	"context"
	"errors"
	"myapp/internal/desktop"
	"myapp/internal/pointer"
	"time"
)

func (c *desktopCapture) runEdges(ctx context.Context) error {
	if c.geometry == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(4 * time.Millisecond)
	defer ticker.Stop()
	watcher := &pointer.Watcher{}
	var screen pointer.Screen
	var nextGeometry time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		state := c.state.Sharing()
		if !state.EdgeEnabled && state.Target == "" {
			watcher.Reset()
			continue
		}
		var err error
		if !time.Now().Before(nextGeometry) {
			screen, err = c.geometry.Screen()
			nextGeometry = time.Now().Add(250 * time.Millisecond)
		}
		if err != nil {
			c.mu.Lock()
			if c.run != nil {
				c.run.cancel()
			}
			c.mu.Unlock()
			c.state.Update(func(s *desktop.Snapshot) { s.Error = err.Error() })
			continue
		}
		c.mu.Lock()
		run := c.run
		var currentScreen string
		if run != nil && run.alive {
			currentScreen = run.sourceScreen.Key()
		}
		positions := make(map[string]string)
		for id, side := range c.positions {
			positions[id] = side
		}
		c.mu.Unlock()
		if currentScreen != "" && currentScreen != screen.Key() {
			c.remoteStopped(run.peer)
			c.state.Update(func(s *desktop.Snapshot) {
				s.Error = "本机主屏幕变化，已停止跨屏控制"
			})
		}
		state = c.state.Sharing()
		if !state.EdgeEnabled || state.Paused || state.Target != "" {
			watcher.Reset()
			continue
		}
		point, err := c.geometry.Position()
		if err != nil {
			continue
		}
		neighbors := make(map[string]string)
		for index, peer := range c.peers {
			if c.links[index].RemoteScreen().Screen.Valid() {
				neighbors[positions[peer.ID]] = peer.ID
			}
		}
		id, entry, hit := watcher.Observe(point, screen, neighbors, c.edgeDelay, time.Now())
		if !hit {
			continue
		}
		request, done := context.WithTimeout(ctx, 8*time.Second)
		err = c.startAt(request, id, &entry)
		nextGeometry = time.Time{}
		done()
		if err != nil {
			c.state.Update(func(s *desktop.Snapshot) { s.Error = err.Error() })
		}
	}
}

func (c *desktopCapture) updateLayout(positions map[string]string) error {
	if len(positions) != len(c.peers) {
		return errors.New("位置数据与已连接电脑不一致")
	}
	seen := make(map[string]bool)
	for _, peer := range c.peers {
		side := positions[peer.ID]
		if side != "left" && side != "right" && side != "up" && side != "down" || seen[side] {
			return errors.New("电脑位置无效或重复")
		}
		seen[side] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.positions = make(map[string]string)
	for id, side := range positions {
		c.positions[id] = side
	}
	return nil
}
