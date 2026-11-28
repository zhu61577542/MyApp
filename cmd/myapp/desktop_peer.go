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
	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/platform"
	"myapp/internal/pointer"
)

type desktopPeerKey struct{}

type desktopPeerRuntime struct {
	mu       sync.Mutex
	device   *peercontrol.Device
	driver   *desktopPeerDriver
	links    map[string]*app.Link
	activity *desktopActivity
}

func withDesktopPeer(ctx context.Context, state *desktop.Session) context.Context {
	if state.Snapshot().Role == config.RoleAgent && !state.Sharing().Paused {
		state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = true })
	}
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
	p.driver.activity = func() *desktopActivity { p.mu.Lock(); defer p.mu.Unlock(); return p.activity }
	p.driver.position = func(id string) (string, bool, bool) {
		p.mu.Lock()
		link := p.links[id]
		p.mu.Unlock()
		if link == nil {
			return "", false, false
		}
		side, confirmed := link.PeerPosition()
		return side, confirmed, link.PeerPositionKnown()
	}
	p.driver.takeover = func(token ownership.Token, reason ownership.Reason) {
		p.mu.Lock()
		link := p.links[token.Source]
		p.mu.Unlock()
		if link != nil {
			if node := link.CurrentPeerControl(); node != nil {
				_ = node.StopToken(token, reason)
			}
		}
	}
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
	lastControlError := ""
	return link.EnablePeerControl(app.PeerOptions{LocalID: state.Snapshot().DeviceID, RemoteID: id, Device: p.device, Changed: func(control ownership.Snapshot) {
		problem := ""
		if node := link.CurrentPeerControl(); node != nil {
			if err := node.ControlError(); err != nil {
				problem = err.Error()
			}
		}
		source, target := "", ""
		if control.Lease != nil {
			source, target = control.Lease.Source, control.Lease.Target
		} else if control.Phase == ownership.Recovering && control.Previous != nil {
			source, target = control.Previous.Source, control.Previous.Target
		}
		current := state.Sharing()
		phase := string(control.Phase)
		paused := p.device.Paused()
		if current.ControlPhase != phase || current.ControlSource != source || current.ControlTarget != target {
			fields := tokenDiagnostic(ownership.Token{Fence: control.Fence})
			fields["phase"], fields["reason"] = phase, control.Reason
			fields["source_local"] = source == state.Snapshot().DeviceID
			state.Diagnostic("控制权变化", fields)
			p.mu.Lock()
			activity := p.activity
			p.mu.Unlock()
			if activity != nil {
				activity.resetEdges()
			}
		}
		activeTarget := ""
		if control.Phase != ownership.Local {
			activeTarget = id
		}
		if current.PeerMode && current.ControlPhase == phase && current.ControlSource == source && current.ControlTarget == target && current.Target == activeTarget && current.Paused == paused && problem == lastControlError && (problem == "" || current.Error == problem) {
			return
		}
		state.Update(func(s *desktop.Snapshot) {
			s.PeerMode = true
			s.ControlPhase = phase
			s.ControlSource = source
			s.ControlTarget = target
			s.Target = activeTarget
			s.Paused = paused
			if problem != "" {
				s.Error = problem
			} else if lastControlError != "" && s.Error == lastControlError {
				s.Error = ""
			}
		})
		lastControlError = problem
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
	sourceScreen       pointer.Screen
	counts             *peerInputCounts
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
	activity       func() *desktopActivity
	position       func(string) (string, bool, bool)
	takeover       func(ownership.Token, ownership.Reason)
	guard          *peerTargetGuard
	guardActivity  *desktopActivity
	targetCounts   *peerInputCounts
}

func (d *desktopPeerDriver) Prepare(ctx context.Context, token ownership.Token, source bool, entry *pointer.Entry, emit func(common.Event) error, stopped func(bool)) (result error) {
	startedAt := time.Now()
	defer func() {
		fields := tokenDiagnostic(token)
		fields["source"], fields["duration_us"], fields["success"] = source, time.Since(startedAt).Microseconds(), result == nil
		d.state.Diagnostic("原生输入准备耗时", fields)
	}()
	state := d.state.Sharing()
	if state.Paused {
		return ownership.ErrPaused
	}
	if source && d.activity != nil {
		activity := d.activity()
		if activity == nil {
			return errors.New("实体输入监视器尚未装配")
		}
		if err := activity.waitReady(ctx); err != nil {
			return err
		}
		activity.clearEdgeIntent()
	}
	if !source {
		if entry != nil && d.position != nil {
			side, confirmed, known := d.position(token.Source)
			if known && (!confirmed || entry.Side != pointer.OppositeSide(side)) {
				return errors.New("跨屏位置已变化，正在等待两端确认")
			}
		}
		var activity *desktopActivity
		if d.activity != nil {
			activity = d.activity()
			if activity == nil {
				return errors.New("实体输入监视器尚未装配")
			}
			if err := activity.waitReady(ctx); err != nil {
				return err
			}
		}
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
		d.targetCounts = &peerInputCounts{token: token}
		i.counts = d.targetCounts
		guard := &peerTargetGuard{token: token, stopped: func(reason ownership.Reason) {
			if reason == ownership.LocalTakeover {
				d.state.ReportEdge(false, "本机实体操作已接管", "")
			} else {
				message := "实体输入监听已中断"
				if activity != nil {
					activity.mu.Lock()
					if activity.err != nil {
						message = activity.err.Error()
					}
					activity.mu.Unlock()
				}
				d.state.Update(func(s *desktop.Snapshot) { s.Error = message })
			}
			if d.takeover != nil {
				d.takeover(token, reason)
			} else {
				stopped(false)
			}
		}}
		d.guard, d.guardActivity = guard, activity
		i.guard = guard
		if activity != nil {
			if err := activity.attachTarget(guard); err != nil {
				d.mu.Unlock()
				return err
			}
		}
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
	screen, err := pointer.SourceScreen(geometry)
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
	run := &peerCaptureRun{token: token, capture: capturer, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), start: make(chan struct{}), geometry: geometry, returnPoint: &position, sourceScreen: screen}
	run.counts = &peerInputCounts{token: token}
	var sourceTopology *pointer.Topology
	if backend, ok := geometry.(pointer.TopologyBackend); ok {
		topology, err := backend.Topology()
		if err != nil {
			cancel()
			return err
		}
		if !topology.Valid() || topology.Bounds.Key() != screen.Key() {
			cancel()
			return errors.New("来源显示器布局已经变化")
		}
		sourceTopology = &topology
	}
	d.mu.Lock()
	d.run = run
	d.mu.Unlock()
	go func() {
		var once sync.Once
		emergency := newEmergencyKeys(func() { stopped(true) })
		filter := func(event common.Event) bool {
			if keyboardEvent(event) {
				run.counts.keyboardCaptured.Add(1)
			}
			if d.activity != nil {
				if activity := d.activity(); activity != nil {
					activity.observeState(event)
				}
			}
			return emergency(event)
		}
		handler := func(_ context.Context, event common.Event) error {
			if travel != nil && event.Kind == common.MouseRelative {
				point, back := travel.Move(int(event.X), int(event.Y))
				if back {
					returnEntry := travel.Entry
					returnEntry.Position = travel.ReturnRatio()
					landing, err := pointer.Landing(screen, returnEntry, true)
					if sourceTopology != nil {
						landing, err = sourceTopology.Landing(returnEntry, true)
					}
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
			err := emit(event)
			if err != nil {
				run.counts.failed.Add(1)
			} else if keyboardEvent(event) {
				run.counts.keyboardSent.Add(1)
			} else {
				run.counts.mouseSent.Add(1)
			}
			return err
		}
		result := captureQueuedAfter(ctx, capturer, handler, func() { once.Do(func() { close(run.ready) }) }, run.start, filter)
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
	guard := d.guard
	d.mu.Unlock()
	if run == nil {
		if guard != nil && guard.revoked.Load() {
			return ownership.ErrStale
		}
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
	hideStarted := time.Now()
	restore, err := run.geometry.Hide(ctx)
	fields := tokenDiagnostic(token)
	fields["duration_us"], fields["success"] = time.Since(hideStarted).Microseconds(), err == nil
	d.state.Diagnostic("来源光标隐藏耗时", fields)
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
	run, i, guard, activity := d.run, d.injector, d.guard, d.guardActivity
	d.mu.Unlock()
	if guard != nil {
		if activity != nil {
			activity.detachTarget(guard)
		} else {
			guard.closed.Store(true)
			guard.revoked.Store(true)
		}
	}
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
		if guard != nil {
			guard.preserve(i.inner)
		}
		result = errors.Join(result, i.Close())
	}
	if result == nil {
		if run != nil {
			run.counts.report(d.state, true, true, run.capture)
		}
		if i != nil {
			i.counts.report(d.state, false, true, nil)
		}
		d.mu.Lock()
		d.run, d.injector = nil, nil
		d.targetToken, d.entry = ownership.Token{}, nil
		d.guard, d.guardActivity = nil, nil
		d.targetCounts = nil
		d.mu.Unlock()
	}
	return result
}

func (d *desktopPeerDriver) Inject(event common.Event) error {
	d.mu.Lock()
	i := d.injector
	guard := d.guard
	counts := d.targetCounts
	d.mu.Unlock()
	if counts != nil && keyboardEvent(event) {
		counts.keyboardReceived.Add(1)
	}
	if guard != nil && guard.revoked.Load() {
		if counts != nil && keyboardEvent(event) {
			counts.keyboardRejected.Add(1)
		}
		return nil
	}
	if i == nil {
		return ownership.ErrStale
	}
	return i.Inject(event)
}
