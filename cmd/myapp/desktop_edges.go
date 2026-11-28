package main

import (
	"context"
	"errors"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
	"time"
)

func (c *desktopCapture) runEdges(ctx context.Context) error {
	if c.activity != nil {
		monitorCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); c.activity.run(monitorCtx) }()
		defer func() { cancel(); <-done }()
	}
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
	var lastMove uint64
	var lastEpoch uint64
	physicalMovement := false
	listening := c.state.Snapshot().Role == config.RoleAgent
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		state := c.state.Sharing()
		peerEdgesReady := false
		if len(c.links) == 1 {
			_, peerEdgesReady = c.links[0].PeerPosition()
		}
		if state.PeerEdgesReady != peerEdgesReady {
			c.state.Update(func(s *desktop.Snapshot) { s.PeerEdgesReady = peerEdgesReady })
		}
		if c.activity != nil {
			epoch := c.activity.epoch.Load()
			if epoch != lastEpoch {
				lastEpoch = epoch
				lastMove = c.activity.edgeBaseline.Load()
				physicalMovement = false
				watcher.Reset()
			}
		}
		if !state.EdgeEnabled && state.Target == "" {
			watcher.Reset()
			continue
		}
		if !time.Now().Before(nextGeometry) {
			if c.activity != nil {
				screen, screenErr = pointer.SourceScreen(c.geometry)
			} else {
				screen, screenErr = c.geometry.Screen()
			}
			nextGeometry = time.Now().Add(250 * time.Millisecond)
		}
		if screenErr != nil {
			c.mu.Lock()
			if c.run != nil {
				c.run.cancel()
			}
			c.mu.Unlock()
			if peer := desktopPeerFrom(c.ctx); peer != nil {
				_ = peer.stop(ctx, ownership.LayoutChanged)
			}
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
		if peer := desktopPeerFrom(c.ctx); peer != nil && currentScreen == "" {
			peer.driver.mu.Lock()
			if active := peer.driver.run; active != nil {
				currentScreen = active.sourceScreen.Key()
			}
			peer.driver.mu.Unlock()
		}
		if currentScreen != "" && currentScreen != screen.Key() {
			if run != nil {
				c.remoteStopped(run.peer)
			} else if peer := desktopPeerFrom(c.ctx); peer != nil {
				_ = peer.stop(ctx, ownership.LayoutChanged)
			}
			c.state.Update(func(s *desktop.Snapshot) {
				s.Error = "本机主屏幕变化，已停止跨屏控制"
			})
		}
		state = c.state.Sharing()
		if !state.EdgeEnabled || state.Paused || state.Target != "" || state.PeerMode && state.ControlPhase != string(ownership.Local) {
			watcher.Reset()
			if c.activity != nil {
				lastMove = c.activity.edgeBaseline.Load()
				physicalMovement = false
			}
			continue
		}
		if c.activity != nil {
			c.activity.mu.Lock()
			ready, failure := c.activity.ready, c.activity.err
			c.activity.mu.Unlock()
			if !ready {
				watcher.Reset()
				message := ""
				if failure != nil {
					message = failure.Error()
				}
				c.state.ReportEdge(false, "等待实体输入监听", message)
				continue
			}
			move := c.activity.moves.Load()
			if peerEdgesReady && captureFailure == "" {
				c.state.ReportEdge(true, "双向边缘监视已就绪", "")
			}
			if move != lastMove {
				physicalMovement = true
			}
			if !physicalMovement {
				continue
			}
			lastMove = move
		}
		point, err := c.geometry.Position()
		if err != nil {
			c.state.ReportEdge(false, "读取本机指针失败", err.Error())
			continue
		}
		neighbors := make(map[string]string)
		for index, peer := range c.peers {
			if c.links[index].RemoteScreen().Screen.Valid() {
				if side, confirmed := c.links[index].PeerPosition(); confirmed {
					positions[peer.ID] = side
				} else if listening {
					continue
				}
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
