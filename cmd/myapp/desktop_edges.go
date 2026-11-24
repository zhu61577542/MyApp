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
		failure := "本机屏幕监视器不可用"
		if c.geometryErr != nil {
			failure = c.geometryErr.Error()
		}
		c.state.ReportEdge(false, "本机显示器不可用", failure)
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(4 * time.Millisecond)
	defer ticker.Stop()
	watcher := &pointer.Watcher{}
	var screen pointer.Screen
	var screenErr error
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
		if !time.Now().Before(nextGeometry) {
			screen, screenErr = c.geometry.Screen()
			nextGeometry = time.Now().Add(250 * time.Millisecond)
		}
		if screenErr != nil {
			c.mu.Lock()
			if c.run != nil {
				c.run.cancel()
			}
			c.mu.Unlock()
			c.state.ReportEdge(false, "读取本机显示器失败", screenErr.Error())
			continue
		}
		c.mu.Lock()
		run := c.run
		var currentScreen string
		if run != nil && run.alive {
			currentScreen = run.sourceScreen.Key()
		}
		positions := make(map[string]string)
		captureFailure := c.captureError
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
			c.state.ReportEdge(false, "读取本机指针失败", err.Error())
			continue
		}
		neighbors := make(map[string]string)
		for index, peer := range c.peers {
			if c.links[index].RemoteScreen().Screen.Valid() {
				neighbors[positions[peer.ID]] = peer.ID
			}
		}
		if len(neighbors) == 0 {
			c.state.ReportEdge(false, "等待对端显示器布局", captureFailure)
		} else if !screen.Contains(point) {
			c.state.ReportEdge(false, "请将鼠标移回本机主屏", captureFailure)
		} else {
			c.state.ReportEdge(true, "边缘监视已就绪", captureFailure)
		}
		id, entry, hit := watcher.Observe(point, screen, neighbors, c.edgeDelay, time.Now())
		if !hit {
			continue
		}
		request, done := context.WithTimeout(ctx, 8*time.Second)
		c.state.ReportEdge(false, "正在准备跨屏", "")
		err = c.startAt(request, id, &entry)
		nextGeometry = time.Time{}
		done()
		if err != nil {
			captureFailure = err.Error()
			c.mu.Lock()
			c.captureError = captureFailure
			c.mu.Unlock()
			c.state.ReportEdge(false, "跨屏启动失败", captureFailure)
			c.state.Update(func(s *desktop.Snapshot) { s.Error = err.Error() })
		} else {
			c.mu.Lock()
			c.captureError = ""
			c.mu.Unlock()
			c.state.ReportEdge(true, "跨屏控制已启动", "")
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
