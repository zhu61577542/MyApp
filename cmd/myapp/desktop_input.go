package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/platform"
	"myapp/internal/pointer"
)

type desktopInjector struct {
	mu         sync.Mutex
	inner      common.Injector
	active     bool
	begun      bool
	blocked    bool
	state      *desktop.Session
	peer       string
	create     func() (common.Injector, error)
	onBlocked  func() error
	geometry   pointer.Backend
	edgeScreen string
	nextCheck  time.Time
}

func (i *desktopInjector) control(_ context.Context, action string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if action == "end" {
		return i.endLocked()
	}
	state := i.state.Sharing()
	if i.blocked || state.Paused || !state.Accepting {
		return errors.New("对端已暂停接受控制")
	}
	if i.inner == nil {
		var err error
		i.inner, err = i.create()
		if err != nil {
			return err
		}
	}
	i.active = true
	i.edgeScreen = ""
	i.nextCheck = time.Time{}
	i.begun = true
	i.state.Update(func(s *desktop.Snapshot) { s.Target = i.peer })
	return nil
}

func (i *desktopInjector) controlEdge(ctx context.Context, action string, entry *pointer.Entry) error {
	if entry == nil {
		return i.control(ctx, action)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.geometry == nil || !entry.Valid() {
		return errors.New("本机尚不支持跨屏入口")
	}
	screen, err := pointer.ScreenWithKey(i.geometry, entry.ScreenKey)
	if err != nil {
		return err
	}
	if screen.Key() != entry.ScreenKey {
		return errors.New("目标屏幕已变化，请重新进入")
	}
	state := i.state.Sharing()
	if i.blocked || state.Paused || !state.Accepting {
		return errors.New("对端已暂停接受控制")
	}
	if i.inner == nil {
		i.inner, err = i.create()
		if err != nil {
			return err
		}
	}
	landing, err := pointer.Landing(screen, *entry, false)
	if err != nil {
		return err
	}
	if topology, e := pointer.TopologyFor(i.geometry); e == nil && topology.Bounds.Key() == entry.ScreenKey {
		travel, e := pointer.NewTopologyTravel(topology, *entry)
		if e != nil {
			return e
		}
		landing = travel.Point
	}
	if err := i.inner.Inject(common.Event{Kind: common.MouseAbsolute, Sequence: 1, X: int32(landing.X), Y: int32(landing.Y)}); err != nil {
		return err
	}
	i.active = true
	i.begun = true
	i.edgeScreen = screen.Key()
	i.nextCheck = time.Now().Add(250 * time.Millisecond)
	i.state.Update(func(s *desktop.Snapshot) { s.Target = i.peer })
	return nil
}

func (i *desktopInjector) endLocked() error {
	i.active = false
	i.edgeScreen = ""
	var err error
	if i.inner != nil {
		err = i.inner.ReleaseAll()
	}
	i.state.Update(func(s *desktop.Snapshot) { s.Target = "" })
	return err
}

func (i *desktopInjector) Inject(event common.Event) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		if i.begun {
			return nil
		}
		return errors.New("对端未完成控制确认，请两端均使用桌面界面后重新连接")
	}
	state := i.state.Sharing()
	if state.Paused || !state.Accepting {
		return i.endLocked()
	}
	check := !time.Now().Before(i.nextCheck)
	if checker, ok := i.inner.(interface{ CanInject() bool }); check && ok && !checker.CanInject() {
		_ = i.endLocked()
		return errors.New("控制键鼠权限已撤销")
	}
	if check && i.edgeScreen != "" {
		screen, err := pointer.ScreenWithKey(i.geometry, i.edgeScreen)
		if err != nil || screen.Key() != i.edgeScreen {
			_ = i.endLocked()
			return errors.New("目标显示器布局变化，已停止控制")
		}
	}
	if check {
		i.nextCheck = time.Now().Add(250 * time.Millisecond)
	}
	return i.inner.Inject(event)
}
func (i *desktopInjector) ReleaseAll() error { i.mu.Lock(); defer i.mu.Unlock(); return i.endLocked() }
func (i *desktopInjector) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	err := i.endLocked()
	if i.inner != nil {
		err = errors.Join(err, i.inner.Close())
	}
	return err
}
func (i *desktopInjector) block(blocked bool) error {
	i.mu.Lock()
	i.blocked = blocked
	wasActive := i.active
	var err error
	if blocked {
		err = i.endLocked()
	}
	i.mu.Unlock()
	if blocked && wasActive && i.onBlocked != nil {
		err = errors.Join(err, i.onBlocked())
	}
	return err
}

type captureRun struct {
	cancel        context.CancelFunc
	done          chan struct{}
	alive         bool
	err           error
	peer          string
	sourceScreen  pointer.Screen
	returnPoint   *pointer.Point
	restoreCursor func() error
}

func (c *desktopCapture) remoteStopped(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped == nil {
		c.stopped = make(map[string]uint64)
	}
	c.stopped[peer]++
	if c.run != nil && c.run.peer == peer {
		c.run.cancel()
	}
}

type desktopCapture struct {
	op          sync.Mutex
	mu          sync.Mutex
	run         *captureRun
	ctx         context.Context
	hub         *app.Hub
	links       []*app.Link
	peers       []config.Peer
	state       *desktop.Session
	create      func() (common.Capturer, error)
	sequence    uint64
	stopped     map[string]uint64
	geometry    pointer.Backend
	geometryErr error
	recoveryErr error
	stopEpoch   uint64
	positions   map[string]string
	edgeDelay   time.Duration
}

func (c *desktopCapture) stop(ctx context.Context) error {
	c.mu.Lock()
	c.stopEpoch++
	if c.run != nil {
		c.run.cancel()
	}
	c.mu.Unlock()
	c.op.Lock()
	defer c.op.Unlock()
	return c.stopLocked(ctx)
}
func (c *desktopCapture) stopLocked(ctx context.Context) error {
	c.mu.Lock()
	run := c.run
	recoveryErr := c.recoveryErr
	c.mu.Unlock()
	if run == nil {
		return recoveryErr
	}
	run.cancel()
	select {
	case <-run.done:
		return run.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *desktopCapture) start(ctx context.Context, id string) error {
	return c.startAt(ctx, id, nil)
}

func (c *desktopCapture) startAt(ctx context.Context, id string, entry *pointer.Entry) error {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	recoveryErr := c.recoveryErr
	startEpoch := c.stopEpoch
	c.mu.Unlock()
	if recoveryErr != nil {
		return fmt.Errorf("上次光标恢复未确认，请重启共享服务: %w", recoveryErr)
	}
	if c.geometryErr != nil && runtime.GOOS != "linux" {
		return fmt.Errorf("无法准备本机光标: %w", c.geometryErr)
	}
	state := c.state.Snapshot()
	if state.Paused {
		return errors.New("请先恢复共享")
	}
	if entry != nil && !state.EdgeEnabled {
		return errors.New("跨屏切换已关闭")
	}
	index := -1
	for j, p := range c.peers {
		if p.ID == id {
			index = j
			break
		}
	}
	if index < 0 {
		return errors.New("没有找到目标电脑")
	}
	if err := c.stopLocked(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	generation := c.stopped[id]
	recoveryErr = c.recoveryErr
	c.mu.Unlock()
	if recoveryErr != nil {
		return fmt.Errorf("上次光标恢复未确认，请重启共享服务: %w", recoveryErr)
	}
	var travel *pointer.Travel
	var sourceScreen pointer.Screen
	var sourcePoint pointer.Point
	if c.geometry != nil {
		var err error
		sourceScreen, err = c.geometry.Screen()
		if err != nil {
			return err
		}
		sourcePoint, err = c.geometry.Position()
		if err != nil {
			return err
		}
	}
	if entry != nil {
		if c.geometry == nil {
			return errors.New("当前平台无法监视屏幕边缘")
		}
		copy := *entry
		copy.ScreenKey = c.links[index].RemoteScreen().Screen.Key()
		var err error
		info := c.links[index].RemoteScreen()
		if info.Topology != nil {
			travel, err = pointer.NewTopologyTravel(*info.Topology, copy)
		} else {
			travel, err = pointer.NewTravel(info.Screen, copy)
		}
		if err != nil {
			return err
		}
	}
	capturer, err := c.create()
	if err != nil {
		_ = c.links[index].RequestControl(ctx, "end")
		return err
	}
	if err := c.hub.Activate(ctx, index); err != nil {
		capturer.Close()
		_ = c.links[index].RequestControl(ctx, "end")
		return err
	}
	captureCtx, cancel := context.WithCancel(c.ctx)
	run := &captureRun{cancel: cancel, done: make(chan struct{}), alive: true, peer: id, sourceScreen: sourceScreen}
	c.mu.Lock()
	latest := c.state.Sharing()
	if generation != c.stopped[id] || startEpoch != c.stopEpoch || latest.Paused || entry != nil && !latest.EdgeEnabled {
		c.mu.Unlock()
		cancel()
		var cursorErr error
		if run.restoreCursor != nil {
			cursorErr = run.restoreCursor()
			c.mu.Lock()
			c.recoveryErr = cursorErr
			c.mu.Unlock()
		}
		_ = capturer.Close()
		_ = c.hub.RecoverLocal(ctx)
		_ = c.links[index].RequestControl(ctx, "end")
		return errors.Join(errors.New("对端已停止接受控制，请恢复后再开始"), cursorErr)
	}
	c.run = run
	c.mu.Unlock()
	ready := make(chan struct{})
	startSending := make(chan struct{})
	handoffDone := make(chan struct{})
	nativeStopped := make(chan struct{})
	var handoffOnce sync.Once
	finishHandoff := func() { handoffOnce.Do(func() { close(handoffDone) }) }
	defer finishHandoff()
	var once sync.Once
	onReady := func() { once.Do(func() { close(ready) }) }
	go func() {
		emergency := newEmergencyKeys(func() { c.state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = false }); cancel() })
		choose := newTargetKeys(len(c.peers), func(next int) error {
			cancel()
			go func() {
				<-run.done
				nextCtx, done := context.WithTimeout(c.ctx, 8*time.Second)
				defer done()
				if err := c.start(nextCtx, c.peers[next].ID); err != nil {
					c.state.Update(func(s *desktop.Snapshot) { s.Error = err.Error() })
				}
			}()
			return nil
		})
		handler := func(eventCtx context.Context, event common.Event) error {
			if consumed, err := choose(event); consumed || err != nil {
				return err
			}
			if travel != nil && event.Kind == common.MouseRelative {
				point, back := travel.Move(int(event.X), int(event.Y))
				if back {
					returnEntry := travel.Entry
					returnEntry.Position = travel.ReturnRatio()
					landing, err := pointer.Landing(sourceScreen, returnEntry, true)
					if err != nil {
						return err
					}
					run.returnPoint = &landing
					cancel()
					return nil
				}
				event.Kind = common.MouseAbsolute
				event.X = int32(point.X)
				event.Y = int32(point.Y)
			}
			c.sequence++
			event.Sequence = c.sequence
			return c.hub.SendInput(eventCtx, event)
		}
		result := captureQueuedAfter(captureCtx, capturer, handler, onReady, startSending, emergency)
		cancel()
		close(nativeStopped)
		<-handoffDone
		if result == context.Canceled {
			result = nil
		}
		select {
		case <-ready:
		default:
			if result == nil {
				result = errors.New("捕获器未完成启动确认")
			}
		}
		cancel()
		result = errors.Join(result, capturer.Close())
		if c.geometry != nil && run.returnPoint != nil {
			result = errors.Join(result, c.geometry.Warp(*run.returnPoint))
		}
		var cursorErr error
		if run.restoreCursor != nil {
			cursorErr = run.restoreCursor()
			result = errors.Join(result, cursorErr)
		}
		c.state.Update(func(s *desktop.Snapshot) { s.Target = "" })
		cleanup, done := context.WithTimeout(c.ctx, time.Second)
		releaseErr := c.hub.RecoverLocal(cleanup)
		endErr := c.links[index].RequestControl(cleanup, "end")
		done()
		if c.ctx.Err() == nil {
			result = errors.Join(result, releaseErr, endErr)
		}
		c.mu.Lock()
		c.recoveryErr = cursorErr
		run.alive = false
		run.err = result
		if c.run == run {
			c.run = nil
		}
		c.state.Update(func(s *desktop.Snapshot) {
			s.Target = ""
			if result != nil {
				if cursorErr != nil {
					s.EdgeEnabled = false
				}
				s.Error = result.Error()
			}
		})
		c.mu.Unlock()
		close(run.done)
	}()
	select {
	case <-ready:
		request, stopRequest := context.WithCancel(ctx)
		stopOnCapture := context.AfterFunc(captureCtx, stopRequest)
		defer func() { stopOnCapture(); stopRequest() }()
		var beginErr error
		if entry != nil {
			copy := travel.Entry
			beginErr = c.links[index].RequestEdgeControl(request, copy)
		} else {
			beginErr = c.links[index].RequestControl(request, "begin")
		}
		latest := c.state.Sharing()
		if beginErr == nil && (latest.Paused || entry != nil && !latest.EdgeEnabled) {
			beginErr = errors.New("控制启动期间已暂停或关闭跨屏")
		}
		if beginErr == nil && c.geometry != nil {
			run.restoreCursor, beginErr = c.geometry.Hide(request)
			if beginErr == nil {
				run.returnPoint = &sourcePoint
			}
		}
		if beginErr == nil {
			beginErr = captureCtx.Err()
		}
		if beginErr != nil {
			cancel()
			finishHandoff()
			<-run.done
			return errors.Join(beginErr, run.err)
		}
		c.state.Update(func(s *desktop.Snapshot) { s.Target = id; s.Error = "" })
		close(startSending)
		finishHandoff()
		return nil
	case <-nativeStopped:
		finishHandoff()
		<-run.done
		if run.err == nil {
			return errors.New("捕获在跨屏确认前结束")
		}
		return run.err
	case <-ctx.Done():
		cancel()
		finishHandoff()
		<-run.done
		return ctx.Err()
	}

}

func newDesktopCapture(ctx context.Context, hub *app.Hub, links []*app.Link, peers []config.Peer, state *desktop.Session) *desktopCapture {
	geometry, err := pointer.New()
	backend, backendErr := platform.NewCapturer()
	create := func() (common.Capturer, error) {
		if backend == nil {
			backend, backendErr = platform.NewCapturer()
		}
		return backend, backendErr
	}
	positions := make(map[string]string)
	for index, peer := range peers {
		position := peer.Position
		if position == "" {
			position = "right"
			if index == 1 {
				position = "left"
			}
		}
		positions[peer.ID] = position
	}
	return &desktopCapture{ctx: ctx, hub: hub, links: links, peers: peers, state: state, create: create, geometry: geometry, geometryErr: err, positions: positions, edgeDelay: 0}
}
