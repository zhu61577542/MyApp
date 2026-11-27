package main

import (
	"context"
	"errors"
	"sync"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/platform"
	"myapp/internal/pointer"
)

type desktopPeerKey struct{}

type desktopPeerRuntime struct {
	mu     sync.Mutex
	device *peercontrol.Device
	driver *desktopPeerDriver
	links  map[string]*app.Link
}

func withDesktopPeer(ctx context.Context, state *desktop.Session) context.Context {
	p := &desktopPeerRuntime{links: make(map[string]*app.Link)}
	p.driver = &desktopPeerDriver{state: state, createCapture: platform.NewCapturer, createInjector: platform.NewInjector, geometry: pointer.New, remote: func(id string) app.ScreenInfo {
		p.mu.Lock()
		link := p.links[id]
		p.mu.Unlock()
		if link != nil {
			return link.RemoteScreen()
		}
		return app.ScreenInfo{}
	}}
	p.device = peercontrol.NewDevice(p.driver)
	return context.WithValue(ctx, desktopPeerKey{}, p)
}
func desktopPeerFrom(ctx context.Context) *desktopPeerRuntime {
	p, _ := ctx.Value(desktopPeerKey{}).(*desktopPeerRuntime)
	return p
}

func enablePeerDesktop(ctx context.Context, link *app.Link, cfg config.Config, id string, state *desktop.Session) error {
	p := desktopPeerFrom(ctx)
	if p == nil || cfg.Role == config.RoleController && len(cfg.Peers) != 1 {
		return nil
	}
	p.mu.Lock()
	p.links[id] = link
	p.mu.Unlock()
	context.AfterFunc(ctx, func() {
		p.mu.Lock()
		if p.links[id] == link {
			delete(p.links, id)
		}
		p.mu.Unlock()
	})
	if state.Sharing().Paused {
		p.device.SetPaused(true)
	}
	return link.EnablePeerControl(app.PeerOptions{LocalID: state.Snapshot().DeviceID, RemoteID: id, Device: p.device, Changed: func(control ownership.Snapshot) {
		source, target := "", ""
		if control.Lease != nil {
			source, target = control.Lease.Source, control.Lease.Target
		} else if control.Phase == ownership.Recovering && control.Previous != nil {
			source, target = control.Previous.Source, control.Previous.Target
		}
		current := state.Sharing()
		phase := string(control.Phase)
		paused := p.device.Paused()
		activeTarget := ""
		if control.Phase != ownership.Local {
			activeTarget = id
		}
		if current.PeerMode && current.ControlPhase == phase && current.ControlSource == source && current.ControlTarget == target && current.Target == activeTarget && current.Paused == paused {
			return
		}
		state.Update(func(s *desktop.Snapshot) {
			s.PeerMode = true
			s.ControlPhase = phase
			s.ControlSource = source
			s.ControlTarget = target
			s.Target = activeTarget
			s.Paused = paused
		})
	}})
}

func (p *desktopPeerRuntime) pause(ctx context.Context, paused bool) error {
	p.device.SetPaused(paused)
	p.mu.Lock()
	links := make([]*app.Link, 0, len(p.links))
	for _, link := range p.links {
		links = append(links, link)
	}
	p.mu.Unlock()
	var result error
	for _, link := range links {
		if node := link.CurrentPeerControl(); node != nil {
			result = errors.Join(result, node.Pause(paused, false))
		}
	}
	if paused {
		result = errors.Join(result, p.device.WaitRestored(ctx))
	}
	return result
}

func (p *desktopPeerRuntime) stop(ctx context.Context, reason ownership.Reason) error {
	p.mu.Lock()
	links := make([]*app.Link, 0, len(p.links))
	for _, link := range p.links {
		links = append(links, link)
	}
	p.mu.Unlock()
	var result error
	for _, link := range links {
		if node := link.CurrentPeerControl(); node != nil {
			result = errors.Join(result, node.Stop(reason))
		}
	}
	result = errors.Join(result, p.device.WaitRestored(ctx))
	return result
}

type peerCaptureRun struct {
	mu                 sync.Mutex
	token              ownership.Token
	capture            common.Capturer
	cancel             context.CancelFunc
	done, ready, start chan struct{}
	started            bool
	err                error
	geometry           pointer.Backend
	returnPoint        *pointer.Point
	restoreCursor      func() error
}

type desktopPeerDriver struct {
	mu             sync.Mutex
	state          *desktop.Session
	createCapture  func() (common.Capturer, error)
	createInjector func() (common.Injector, error)
	geometry       func() (pointer.Backend, error)
	remote         func(string) app.ScreenInfo
	run            *peerCaptureRun
	injector       *desktopInjector
	targetToken    ownership.Token
	entry          *pointer.Entry
}

func (d *desktopPeerDriver) Prepare(ctx context.Context, token ownership.Token, source bool, entry *pointer.Entry, emit func(common.Event) error, stopped func(bool)) error {
	state := d.state.Sharing()
	if state.Paused {
		return ownership.ErrPaused
	}
	if !source {
		geometry, err := d.geometry()
		if err != nil && entry != nil {
			return err
		}
		if entry != nil {
			screen, err := pointer.ScreenWithKey(geometry, entry.ScreenKey)
			if err != nil {
				return err
			}
			if _, err := pointer.Landing(screen, *entry, false); err != nil {
				return err
			}
		}
		i := &desktopInjector{state: d.state, peer: token.Source, create: d.createInjector, geometry: geometry}
		d.mu.Lock()
		d.injector = i
		d.targetToken = token
		if entry != nil {
			copy := *entry
			d.entry = &copy
		} else {
			d.entry = nil
		}
		d.mu.Unlock()
		if err := i.control(ctx, "begin"); err != nil {
			return err
		}
		return peerInjectorReady(i)
	}
	if entry != nil && !state.EdgeEnabled {
		return errors.New("跨屏切换已关闭")
	}
	geometry, err := d.geometry()
	if err != nil {
		return err
	}
	screen, err := geometry.Screen()
	if err != nil {
		return err
	}
	position, err := geometry.Position()
	if err != nil {
		return err
	}
	var travel *pointer.Travel
	if entry != nil {
		info := d.remote(token.Target)
		if info.Topology != nil {
			travel, err = pointer.NewTopologyTravel(*info.Topology, *entry)
		} else {
			travel, err = pointer.NewTravel(info.Screen, *entry)
		}
		if err != nil {
			return err
		}
	}
	capturer, err := d.createCapture()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &peerCaptureRun{token: token, capture: capturer, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), start: make(chan struct{}), geometry: geometry, returnPoint: &position}
	d.mu.Lock()
	d.run = run
	d.mu.Unlock()
	go func() {
		var once sync.Once
		emergency := newEmergencyKeys(func() { stopped(true) })
		handler := func(_ context.Context, event common.Event) error {
			if travel != nil && event.Kind == common.MouseRelative {
				point, back := travel.Move(int(event.X), int(event.Y))
				if back {
					returnEntry := travel.Entry
					returnEntry.Position = travel.ReturnRatio()
					landing, err := pointer.Landing(screen, returnEntry, true)
					if err != nil {
						return err
					}
					run.mu.Lock()
					run.returnPoint = &landing
					run.mu.Unlock()
					stopped(false)
					return context.Canceled
				}
				event.Kind, event.X, event.Y = common.MouseAbsolute, int32(point.X), int32(point.Y)
			}
			return emit(event)
		}
		result := captureQueuedAfter(ctx, capturer, handler, func() { once.Do(func() { close(run.ready) }) }, run.start, emergency)
		run.mu.Lock()
		run.err = result
		run.mu.Unlock()
		close(run.done)
		if ctx.Err() == nil {
			if result != nil {
				d.state.Update(func(s *desktop.Snapshot) { s.Error = result.Error() })
				d.state.ReportEdge(false, "输入捕获异常", result.Error())
			}
			stopped(false)
		}
	}()
	select {
	case <-run.ready:
		return nil
	case <-run.done:
		run.mu.Lock()
		err := run.err
		run.mu.Unlock()
		return errors.Join(errors.New("捕获器在就绪前退出"), err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *desktopPeerDriver) Activate(ctx context.Context, token ownership.Token) error {
	d.mu.Lock()
	run := d.run
	i, targetToken, entry := d.injector, d.targetToken, d.entry
	d.mu.Unlock()
	if run == nil {
		if i == nil || targetToken != token {
			return ownership.ErrStale
		}
		if err := peerInjectorReady(i); err != nil {
			return err
		}
		if entry != nil {
			return i.controlEdge(ctx, "begin", entry)
		}
		return nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.token != token {
		return ownership.ErrStale
	}
	select {
	case <-run.done:
		return errors.New("捕获器已停止")
	default:
	}
	if run.started {
		return nil
	}
	restore, err := run.geometry.Hide(ctx)
	if err != nil {
		return err
	}
	run.restoreCursor, run.started = restore, true
	close(run.start)
	return nil
}

func peerInjectorReady(i *desktopInjector) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inner == nil {
		return errors.New("键鼠注入器尚未准备完成")
	}
	if checker, ok := i.inner.(interface{ CanInject() bool }); ok && !checker.CanInject() {
		return errors.New("控制键鼠权限尚未授予")
	}
	return nil
}

func (d *desktopPeerDriver) Restore(ctx context.Context) error {
	d.mu.Lock()
	run, i := d.run, d.injector
	d.mu.Unlock()
	var result error
	if run != nil {
		run.cancel()
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		result = errors.Join(result, run.capture.Close())
		run.mu.Lock()
		if run.returnPoint != nil {
			result = errors.Join(result, run.geometry.Warp(*run.returnPoint))
		}
		if run.restoreCursor != nil {
			result = errors.Join(result, run.restoreCursor())
		}
		run.mu.Unlock()
	}
	if i != nil {
		result = errors.Join(result, i.Close())
	}
	if result == nil {
		d.mu.Lock()
		d.run, d.injector = nil, nil
		d.targetToken, d.entry = ownership.Token{}, nil
		d.mu.Unlock()
	}
	return result
}

func (d *desktopPeerDriver) Inject(event common.Event) error {
	d.mu.Lock()
	i := d.injector
	d.mu.Unlock()
	if i == nil {
		return ownership.ErrStale
	}
	return i.Inject(event)
}
