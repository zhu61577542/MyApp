package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	common "myapp/internal/input"
	"myapp/internal/ownership"
)

type desktopActivity struct {
	create       func() (common.PhysicalObserver, error)
	mu           sync.Mutex
	ready        bool
	err          error
	keys         [256]bool
	buttons      [8]bool
	moves        atomic.Uint64
	epoch        atomic.Uint64
	edgeBaseline atomic.Uint64
	target       atomic.Pointer[peerTargetGuard]
	motion       common.Event
	pendingMove  time.Time
}

func (a *desktopActivity) resetEdges() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.edgeBaseline.Store(a.moves.Load())
	a.epoch.Add(1)
}

func (a *desktopActivity) clearEdgeIntent() {
	a.mu.Lock()
	a.pendingMove = time.Time{}
	a.mu.Unlock()
}

func (a *desktopActivity) resetUserEdges() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingMove = time.Time{}
	a.edgeBaseline.Store(a.moves.Load())
	a.epoch.Add(1)
}

func (a *desktopActivity) edgeMotion(last uint64) (common.Event, uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	move := a.moves.Load()
	pending := !a.pendingMove.IsZero() && time.Since(a.pendingMove) < 3*time.Second
	a.pendingMove = time.Time{}
	return a.motion, move, move != last || pending
}

func (a *desktopActivity) run(ctx context.Context) {
	for ctx.Err() == nil {
		observer, err := a.create()
		if err == nil {
			err = observer.Observe(ctx, a.observe, func() {
				a.mu.Lock()
				a.ready, a.err = true, nil
				a.mu.Unlock()
			})
			err = errors.Join(err, observer.Close())
		}
		if err == nil && ctx.Err() == nil {
			err = errors.New("实体输入监听已停止")
		}
		a.mu.Lock()
		a.ready, a.err = false, err
		if guard := a.target.Load(); guard != nil && ctx.Err() == nil {
			guard.stop(ownership.PermissionLost)
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (a *desktopActivity) attachTarget(guard *peerTargetGuard) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready {
		if a.err != nil {
			return a.err
		}
		return errors.New("实体输入监听尚未就绪")
	}
	for code, down := range a.keys {
		guard.keys[code].Store(down)
	}
	for code, down := range a.buttons {
		guard.buttons[code].Store(down)
	}
	if !a.target.CompareAndSwap(nil, guard) {
		return ownership.ErrBusy
	}
	a.pendingMove = time.Time{}
	return nil
}

func (a *desktopActivity) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		ready, err := a.ready, a.err
		a.mu.Unlock()
		if ready {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *desktopActivity) observe(event common.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observeStateLocked(event)
	switch event.Kind {
	case common.MouseAbsolute, common.MouseRelative:
		a.motion = event
		a.moves.Add(1)
	}
	if guard := a.target.Load(); guard != nil {
		guard.observe(event)
	}
}

func (a *desktopActivity) observeState(event common.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observeStateLocked(event)
}

func (a *desktopActivity) observeStateLocked(event common.Event) {
	switch event.Kind {
	case common.KeyDown, common.KeyUp:
		if event.Code < 256 {
			a.keys[event.Code] = event.Kind == common.KeyDown
		}
	case common.MouseButtonDown, common.MouseButtonUp:
		if event.Code < 8 {
			a.buttons[event.Code] = event.Kind == common.MouseButtonDown
		}
	}
}

func (a *desktopActivity) detachTarget(guard *peerTargetGuard) {
	a.mu.Lock()
	defer a.mu.Unlock()
	guard.closed.Store(true)
	guard.revoked.Store(true)
	if a.target.CompareAndSwap(guard, nil) && guard.localTakeover.Load() && guard.mouseTakeover.Load() {
		a.pendingMove = time.Now()
	}
}

type peerTargetGuard struct {
	token           ownership.Token
	closed, revoked atomic.Bool
	localTakeover   atomic.Bool
	mouseTakeover   atomic.Bool
	keys            [256]atomic.Bool
	buttons         [8]atomic.Bool
	stopped         func(ownership.Reason)
}

func (g *peerTargetGuard) observe(event common.Event) {
	if g.closed.Load() {
		return
	}
	switch event.Kind {
	case common.KeyDown, common.KeyUp:
		if event.Code < 256 {
			g.keys[event.Code].Store(event.Kind == common.KeyDown)
		}
	case common.MouseButtonDown, common.MouseButtonUp:
		if event.Code < 8 {
			g.buttons[event.Code].Store(event.Kind == common.MouseButtonDown)
		}
	}
	switch event.Kind {
	case common.MouseAbsolute, common.MouseRelative:
		g.mouseTakeover.Store(true)
		g.stop(ownership.LocalTakeover)
	case common.MouseButtonDown, common.MouseScroll, common.KeyDown:
		g.stop(ownership.LocalTakeover)
	}
}

func (g *peerTargetGuard) stop(reason ownership.Reason) {
	if !g.closed.Load() && g.revoked.CompareAndSwap(false, true) {
		g.localTakeover.Store(reason == ownership.LocalTakeover)
		go func() {
			if !g.closed.Load() {
				g.stopped(reason)
			}
		}()
	}
}

func (g *peerTargetGuard) preserve(injector common.Injector) {
	if p, ok := injector.(common.PhysicalPreserver); ok {
		for code := 1; code < len(g.keys); code++ {
			if g.keys[code].Load() {
				p.PreservePhysical(common.Event{Kind: common.KeyDown, Code: uint16(code)})
			}
		}
		for code := 1; code < len(g.buttons); code++ {
			if g.buttons[code].Load() {
				p.PreservePhysical(common.Event{Kind: common.MouseButtonDown, Code: uint16(code)})
			}
		}
	}
}
