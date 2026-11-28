package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

type stage3Observer struct {
	events  chan common.Event
	failure chan error
	closed  atomic.Bool
}

func (o *stage3Observer) Observe(ctx context.Context, activity func(common.Event), ready func()) error {
	ready()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-o.failure:
			return err
		case event := <-o.events:
			activity(event)
		}
	}
}
func (o *stage3Observer) Close() error { o.closed.Store(true); return nil }

func TestActivityObserverFailureClosesGateAndIsJoined(t *testing.T) {
	o := &stage3Observer{events: make(chan common.Event), failure: make(chan error, 1)}
	a := &desktopActivity{create: func() (common.PhysicalObserver, error) { return o, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.run(ctx) }()
	if err := a.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	reason := make(chan ownership.Reason, 1)
	g := &peerTargetGuard{stopped: func(r ownership.Reason) { reason <- r }}
	a.target.Store(g)
	o.failure <- errors.New("监听权限撤销")
	select {
	case r := <-reason:
		if r != ownership.PermissionLost || !g.revoked.Load() {
			t.Fatal(r)
		}
	case <-ctx.Done():
		t.Fatal("监听退出未关闭本机门禁")
	}
	if err := a.waitReady(ctx); err == nil {
		t.Fatal("监听退出后仍报告就绪")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("没有等待监听退出")
	}
	if !o.closed.Load() {
		t.Fatal("原生监听未关闭")
	}
}

type stage3TrackedInjector struct {
	peerNativeInjector
	tracker  *common.Tracker
	released []common.Event
}

func (i *stage3TrackedInjector) Inject(event common.Event) error {
	i.tracker.Apply(event)
	return i.peerNativeInjector.Inject(event)
}
func (i *stage3TrackedInjector) PreservePhysical(event common.Event) {
	i.tracker.PreservePhysical(event)
}
func (i *stage3TrackedInjector) ReleaseAll() error {
	i.released, _ = i.tracker.ReleaseEvents(1)
	return nil
}

func TestPhysicalTakeoverRejectsOldInputAndPreservesFirstLocalKey(t *testing.T) {
	a := &desktopActivity{ready: true}
	i := &stage3TrackedInjector{tracker: common.NewTracker()}
	d := &desktopPeerDriver{state: desktop.New(config.Default("target", config.RoleAgent), "b"), activity: func() *desktopActivity { return a }, geometry: func() (pointer.Backend, error) { return &peerNativePointer{}, nil }, createInjector: func() (common.Injector, error) { return i, nil }}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	ctx := context.Background()
	stopped := make(chan bool, 1)
	if err := d.Prepare(ctx, token, false, nil, nil, func(v bool) { stopped <- v }); err != nil {
		t.Fatal(err)
	}
	if err := d.Activate(ctx, token); err != nil {
		t.Fatal(err)
	}
	for _, code := range []uint16{224, 225} {
		if err := d.Inject(common.Event{Kind: common.KeyDown, Sequence: 1, Code: code}); err != nil {
			t.Fatal(err)
		}
	}
	old := d.guard
	a.observe(common.Event{Kind: common.KeyDown, Code: 224})
	if err := d.Inject(common.Event{Kind: common.MouseRelative, Sequence: 2, X: 1}); err != nil {
		t.Fatal(err)
	}
	if i.events.Load() != 2 {
		t.Fatal("接管后仍注入远端输入")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("接管回调阻塞")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if len(i.released) != 1 || i.released[0].Code != 225 {
		t.Fatal("接管首键被释放", i.released)
	}
	old.observe(common.Event{Kind: common.MouseRelative, X: 1})
	if a.target.Load() != nil {
		t.Fatal("恢复未解除旧租约订阅")
	}
	if err := d.Prepare(ctx, token, false, nil, nil, func(bool) { t.Error("旧监听影响新控制") }); err != nil {
		t.Fatal(err)
	}
	if d.guard.revoked.Load() {
		t.Fatal("新租约被旧回调撤销")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTargetCannotAttachAfterReadinessDisappears(t *testing.T) {
	a := &desktopActivity{ready: true}
	d := &desktopPeerDriver{state: desktop.New(config.Default("target", config.RoleAgent), "b"), activity: func() *desktopActivity { return a }, geometry: func() (pointer.Backend, error) {
		a.mu.Lock()
		a.ready = false
		a.err = errors.New("监听已退出")
		a.mu.Unlock()
		return &peerNativePointer{}, nil
	}, createInjector: func() (common.Injector, error) { return &peerNativeInjector{}, nil }}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	if err := d.Prepare(context.Background(), token, false, nil, nil, func(bool) {}); err == nil {
		t.Fatal("监听已退出仍能报告目标就绪")
	}
	if a.target.Load() != nil {
		t.Fatal("就绪空洞中挂载了目标保护")
	}
	if err := d.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldPhysicalModifierBeforeReceivingIsNotReleased(t *testing.T) {
	a := &desktopActivity{ready: true}
	a.observe(common.Event{Kind: common.KeyDown, Code: 224})
	a.observe(common.Event{Kind: common.MouseButtonDown, Code: 1})
	guard := &peerTargetGuard{stopped: func(ownership.Reason) {}}
	if err := a.attachTarget(guard); err != nil {
		t.Fatal(err)
	}
	i := &stage3TrackedInjector{tracker: common.NewTracker()}
	for _, event := range []common.Event{{Kind: common.KeyDown, Code: 224}, {Kind: common.KeyDown, Code: 225}, {Kind: common.MouseButtonDown, Code: 1}} {
		if err := i.Inject(event); err != nil {
			t.Fatal(err)
		}
	}
	a.observe(common.Event{Kind: common.MouseAbsolute, X: 20, Y: 20})
	a.detachTarget(guard)
	guard.preserve(i)
	if err := i.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if len(i.released) != 1 || i.released[0].Code != 225 {
		t.Fatal("松开了接收前已按住的本机键或按钮", i.released)
	}
}
