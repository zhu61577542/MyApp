package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

type peerNativePointer struct {
	hidden, restored, warped atomic.Int64
	id                       string
}

func (p *peerNativePointer) Screen() (pointer.Screen, error) {
	id := p.id
	if id == "" {
		id = "native-test"
	}
	return pointer.Screen{ID: id, Width: 100, Height: 100}, nil
}
func (*peerNativePointer) Position() (pointer.Point, error) { return pointer.Point{X: 40, Y: 50}, nil }
func (p *peerNativePointer) Warp(pointer.Point) error       { p.warped.Add(1); return nil }
func (p *peerNativePointer) Hide(context.Context) (func() error, error) {
	p.hidden.Add(1)
	var once sync.Once
	return func() error { once.Do(func() { p.restored.Add(1) }); return nil }, nil
}

type peerNativeInjector struct {
	events, releases atomic.Int64
	denied           atomic.Bool
}

func (i *peerNativeInjector) Inject(common.Event) error { i.events.Add(1); return nil }
func (i *peerNativeInjector) ReleaseAll() error         { i.releases.Add(1); return nil }
func (*peerNativeInjector) Close() error                { return nil }
func (i *peerNativeInjector) CanInject() bool           { return !i.denied.Load() }

type peerNativeCapture struct {
	events chan common.Event
	fail   <-chan struct{}
}

func (c *peerNativeCapture) Capture(ctx context.Context, f func(common.Event) error) error {
	return c.CaptureWithReady(ctx, f, nil)
}
func (c *peerNativeCapture) CaptureWithReady(ctx context.Context, f func(common.Event) error, ready func()) error {
	if ready != nil {
		ready()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.fail:
			return errInputCongestion
		case e := <-c.events:
			if err := f(e); err != nil {
				return err
			}
		}
	}
}
func (*peerNativeCapture) Close() error { return nil }
func nativeWait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("原生适配状态等待超时")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPeerTargetPrepareDoesNotMoveCursorAndChecksPermission(t *testing.T) {
	state := desktop.New(config.Default("target", config.RoleAgent), "b")
	geometry := &peerNativePointer{}
	injector := &peerNativeInjector{}
	d := &desktopPeerDriver{state: state, geometry: func() (pointer.Backend, error) { return geometry, nil }, createInjector: func() (common.Injector, error) { return injector, nil }}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	screen, _ := geometry.Screen()
	entry := &pointer.Entry{Side: "right", Position: 0.5, ScreenKey: screen.Key()}
	ctx := context.Background()
	if err := d.Prepare(ctx, token, false, entry, nil, nil); err != nil {
		t.Fatal(err)
	}
	if injector.events.Load() != 0 {
		t.Fatal("双方就绪前已定位目标光标")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if injector.events.Load() != 0 {
		t.Fatal("准备期取消仍移动目标光标")
	}
	injector.denied.Store(true)
	if err := d.Prepare(ctx, token, false, entry, nil, nil); err == nil {
		t.Fatal("无注入权限仍报告Ready")
	}
	if injector.events.Load() != 0 {
		t.Fatal("权限检查前注入")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	injector.denied.Store(false)
	if err := d.Prepare(ctx, token, false, entry, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Activate(ctx, token); err != nil {
		t.Fatal(err)
	}
	if injector.events.Load() != 1 {
		t.Fatal("激活时未定位入口")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPeerSourceBuffersUntilActivatedAndRestores(t *testing.T) {
	state := desktop.New(config.Default("source", config.RoleController), "a")
	geometry := &peerNativePointer{}
	capture := &peerNativeCapture{events: make(chan common.Event, 8)}
	d := &desktopPeerDriver{state: state, geometry: func() (pointer.Backend, error) { return geometry, nil }, createCapture: func() (common.Capturer, error) { return capture, nil }}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var emitted atomic.Int64
	if err := d.Prepare(ctx, token, true, nil, func(common.Event) error { emitted.Add(1); return nil }, func(bool) {}); err != nil {
		t.Fatal(err)
	}
	capture.events <- common.Event{Kind: common.KeyDown, Code: 4}
	time.Sleep(10 * time.Millisecond)
	if emitted.Load() != 0 || geometry.hidden.Load() != 0 {
		t.Fatal("激活前转发或隐藏光标")
	}
	if err := d.Activate(ctx, token); err != nil {
		t.Fatal(err)
	}
	nativeWait(t, func() bool { return emitted.Load() == 1 })
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if geometry.hidden.Load() != 1 || geometry.restored.Load() != 1 || geometry.warped.Load() != 1 {
		t.Fatal("本机光标未完整恢复")
	}
}

func TestPeerCaptureErrorRemainsVisible(t *testing.T) {
	state := desktop.New(config.Default("source", config.RoleController), "a")
	geometry := &peerNativePointer{}
	fail := make(chan struct{})
	d := &desktopPeerDriver{state: state, geometry: func() (pointer.Backend, error) { return geometry, nil }, createCapture: func() (common.Capturer, error) {
		return &peerNativeCapture{events: make(chan common.Event), fail: fail}, nil
	}}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopped := make(chan bool, 1)
	if err := d.Prepare(ctx, token, true, nil, func(common.Event) error { return nil }, func(b bool) { stopped <- b }); err != nil {
		t.Fatal(err)
	}
	close(fail)
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("异常未通知控制会话")
	}
	if state.Snapshot().Error != errInputCongestion.Error() {
		t.Fatal("捕获错误被静默清除")
	}
	if err := d.Restore(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPeerDesktopAssemblySupportsBothRoleDirections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var contexts [2]context.Context
	var links [2]*app.Link
	var runtimes [2]*desktopPeerRuntime
	var states [2]*desktop.Session
	var injectors [2]*peerNativeInjector
	var geometries [2]*peerNativePointer
	ids := [2]string{"a-mac", "b-windows"}
	roles := [2]config.Role{config.RoleController, config.RoleAgent}
	for i := range links {
		cfg := config.Default(ids[i], roles[i])
		cfg.Peers = []config.Peer{{ID: ids[1-i], Address: "127.0.0.1:24800"}}
		states[i] = desktop.New(cfg, ids[i])
		contexts[i] = withDesktopPeer(desktop.WithSession(ctx, states[i]), states[i])
		runtimes[i] = desktopPeerFrom(contexts[i])
		injectors[i] = &peerNativeInjector{}
		geometries[i] = &peerNativePointer{}
		runtimes[i].driver.createInjector = func() (common.Injector, error) { return injectors[i], nil }
		runtimes[i].driver.createCapture = func() (common.Capturer, error) { return &peerNativeCapture{events: make(chan common.Event, 8)}, nil }
		runtimes[i].driver.geometry = func() (pointer.Backend, error) { return geometries[i], nil }
		links[i], _ = app.NewSplitLink(nil, nil)
		if err := enableDesktopLink(contexts[i], links[i], cfg, desktop.Peer{ID: ids[1-i]}, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, b := net.Pipe()
	x, y := net.Pipe()
	ended := make(chan error, 2)
	go func() { ended <- links[0].RunChannels(ctx, a, x) }()
	go func() { ended <- links[1].RunChannels(ctx, b, y) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Error("适配连接未退出")
			}
		}
	})
	for source := range links {
		capture := &desktopCapture{ctx: contexts[source], links: []*app.Link{links[source]}, peers: []config.Peer{{ID: ids[1-source]}}, state: states[source]}
		unbind := bindDesktop(contexts[source], nil, nil, capture, nil)
		if err := states[source].Apply(ctx, desktop.Command{Version: desktop.Version, ID: 1, Action: "start_control", Target: ids[1-source]}); err != nil {
			t.Fatal(err)
		}
		if err := links[source].SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
			t.Fatal(err)
		}
		nativeWait(t, func() bool { return injectors[1-source].events.Load() == 1 })
		if err := states[source].Apply(ctx, desktop.Command{Version: desktop.Version, ID: 2, Action: "stop_control"}); err != nil {
			t.Fatal(err)
		}
		nativeWait(t, func() bool {
			for _, link := range links {
				node := link.CurrentPeerControl()
				if node == nil || node.Snapshot().Phase != ownership.Local {
					return false
				}
			}
			return true
		})
		if geometries[source].restored.Load() != 1 {
			t.Fatal("来源未恢复光标")
		}
		unbind()
	}
	for _, node := range []int{0, 1} {
		if !states[node].Snapshot().PeerMode {
			nativeWait(t, func() bool { return states[node].Snapshot().PeerMode })
		}
	}
	t.Log("原生适配工厂通过真实Link连接双向装配；测试替身未操作实体键鼠")
}

type peerFailReleaseInjector struct{ peerNativeInjector }

func (*peerFailReleaseInjector) ReleaseAll() error { return errors.New("native release failed") }

func TestPeerLegacyReleaseFailureBlocksSubsequentControl(t *testing.T) {
	state := desktop.New(config.Default("legacy", config.RoleAgent), "local")
	ctx := withDesktopPeer(context.Background(), state)
	peer := desktopPeerFrom(ctx)
	i := &desktopInjector{state: state, peer: "remote", create: func() (common.Injector, error) { return &peerFailReleaseInjector{}, nil }, onRecoveryError: peer.device.BlockRecovery}
	if err := i.control(ctx, "begin"); err != nil {
		t.Fatal(err)
	}
	if err := i.ReleaseAll(); err == nil {
		t.Fatal("释放失败未报告")
	}
	if err := i.control(ctx, "begin"); err == nil {
		t.Fatal("原连接重复控制绕过恢复失败")
	}
	if peer.device.RecoveryError() == nil {
		t.Fatal("恢复失败未保留在连接之外")
	}
}

func TestPeerEdgeUsesTargetScreenKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var contexts [2]context.Context
	var links [2]*app.Link
	var states [2]*desktop.Session
	var injectors [2]*peerNativeInjector
	geometry := [2]*peerNativePointer{{id: "mac-source"}, {id: "windows-target"}}
	ids := [2]string{"a-mac", "b-windows"}
	for i := range links {
		cfg := config.Default(ids[i], []config.Role{config.RoleController, config.RoleAgent}[i])
		cfg.Peers = []config.Peer{{ID: ids[1-i], Position: "left"}}
		states[i] = desktop.New(cfg, ids[i])
		contexts[i] = withDesktopPeer(desktop.WithSession(ctx, states[i]), states[i])
		peer := desktopPeerFrom(contexts[i])
		injectors[i] = &peerNativeInjector{}
		peer.driver.geometry = func() (pointer.Backend, error) { return geometry[i], nil }
		peer.driver.createCapture = func() (common.Capturer, error) { return &peerNativeCapture{events: make(chan common.Event, 8)}, nil }
		peer.driver.createInjector = func() (common.Injector, error) { return injectors[i], nil }
		links[i], _ = app.NewSplitLink(nil, nil)
		if err := enableDesktopLink(contexts[i], links[i], cfg, desktop.Peer{ID: ids[1-i]}, nil); err != nil {
			t.Fatal(err)
		}
		links[i].EnableScreen(geometry[i].Screen, nil)
	}
	a, b := net.Pipe()
	x, y := net.Pipe()
	ended := make(chan error, 2)
	go func() { ended <- links[0].RunChannels(ctx, a, x) }()
	go func() { ended <- links[1].RunChannels(ctx, b, y) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Error("跨屏测试连接未退出")
			}
		}
	})
	nativeWait(t, func() bool { return links[0].RemoteScreen().Screen.Valid() })
	screen, _ := geometry[0].Screen()
	entry := pointer.Entry{Side: "left", Position: 0.5, ScreenKey: screen.Key()}
	capture := &desktopCapture{ctx: contexts[0], state: states[0], links: []*app.Link{links[0]}, peers: []config.Peer{{ID: ids[1]}}}
	if err := capture.startAt(ctx, ids[1], &entry); err != nil {
		t.Fatalf("两端不同屏幕标识导致触边失败: %v", err)
	}
	if entry.ScreenKey != screen.Key() {
		t.Fatal("修改了来源屏幕快照")
	}
	if err := links[0].SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
		t.Fatal(err)
	}
	nativeWait(t, func() bool { return injectors[1].events.Load() == 2 })
	if err := capture.stop(ctx); err != nil {
		t.Fatal(err)
	}
}
