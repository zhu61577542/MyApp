package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/platform"
)

type desktopInjector struct {
	mu        sync.Mutex
	inner     common.Injector
	active    bool
	begun     bool
	blocked   bool
	state     *desktop.Session
	peer      string
	create    func() (common.Injector, error)
	onBlocked func() error
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
	i.begun = true
	i.state.Update(func(s *desktop.Snapshot) { s.Target = i.peer })
	return nil
}

func (i *desktopInjector) endLocked() error {
	i.active = false
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
	if checker, ok := i.inner.(interface{ CanInject() bool }); ok && !checker.CanInject() {
		_ = i.endLocked()
		return errors.New("控制键鼠权限已撤销")
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
	cancel context.CancelFunc
	done   chan struct{}
	alive  bool
	err    error
	peer   string
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
	op       sync.Mutex
	mu       sync.Mutex
	run      *captureRun
	ctx      context.Context
	hub      *app.Hub
	links    []*app.Link
	peers    []config.Peer
	state    *desktop.Session
	create   func() (common.Capturer, error)
	sequence uint64
	stopped  map[string]uint64
}

func (c *desktopCapture) stop(ctx context.Context) error {
	c.op.Lock()
	defer c.op.Unlock()
	return c.stopLocked(ctx)
}
func (c *desktopCapture) stopLocked(ctx context.Context) error {
	c.mu.Lock()
	run := c.run
	c.mu.Unlock()
	if run == nil {
		return nil
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
	c.op.Lock()
	defer c.op.Unlock()
	state := c.state.Snapshot()
	if state.Paused {
		return errors.New("请先恢复共享")
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
	c.mu.Unlock()
	if err := c.links[index].RequestControl(ctx, "begin"); err != nil {
		return err
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
	run := &captureRun{cancel: cancel, done: make(chan struct{}), alive: true, peer: id}
	c.mu.Lock()
	if generation != c.stopped[id] {
		c.mu.Unlock()
		cancel()
		_ = capturer.Close()
		_ = c.hub.RecoverLocal(ctx)
		_ = c.links[index].RequestControl(ctx, "end")
		return errors.New("对端已停止接受控制，请恢复后再开始")
	}
	c.run = run
	c.mu.Unlock()
	ready := make(chan struct{})
	var once sync.Once
	onReady := func() { once.Do(func() { close(ready) }) }
	go func() {
		emergency := newEmergencyKeys(cancel)
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
		handler := func(event common.Event) error {
			if emergency(event) {
				return nil
			}
			if consumed, err := choose(event); consumed || err != nil {
				return err
			}
			c.sequence++
			event.Sequence = c.sequence
			return c.hub.SendInput(captureCtx, event)
		}
		var result error
		if native, ok := capturer.(common.ReadyCapturer); ok {
			result = native.CaptureWithReady(captureCtx, handler, onReady)
		} else {
			result = errors.New("当前捕获器不支持安全启动确认")
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
		cleanup, done := context.WithTimeout(c.ctx, time.Second)
		releaseErr := c.hub.RecoverLocal(cleanup)
		endErr := c.links[index].RequestControl(cleanup, "end")
		done()
		if errors.Is(result, context.Canceled) {
			result = nil
		}
		if c.ctx.Err() == nil {
			result = errors.Join(result, releaseErr, endErr)
		}
		c.mu.Lock()
		run.alive = false
		run.err = result
		c.state.Update(func(s *desktop.Snapshot) {
			s.Target = ""
			if result != nil {
				s.Error = result.Error()
			}
		})
		c.mu.Unlock()
		close(run.done)
	}()
	select {
	case <-ready:
		if captureCtx.Err() != nil {
			<-run.done
			return errors.New("控制启动期间已被停止")
		}
		c.mu.Lock()
		alive := run.alive
		if alive {
			c.state.Update(func(s *desktop.Snapshot) { s.Target = id; s.Error = "" })
		}
		c.mu.Unlock()
		if !alive {
			<-run.done
			return run.err
		}
		return nil
	case <-run.done:
		return run.err
	case <-ctx.Done():
		cancel()
		<-run.done
		return ctx.Err()
	}
}

func newDesktopCapture(ctx context.Context, hub *app.Hub, links []*app.Link, peers []config.Peer, state *desktop.Session) *desktopCapture {
	return &desktopCapture{ctx: ctx, hub: hub, links: links, peers: peers, state: state, create: platform.NewCapturer}
}
