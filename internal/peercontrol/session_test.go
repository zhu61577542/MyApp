package peercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

type testDriver struct {
	mu                            sync.Mutex
	ready                         chan struct{}
	inputs                        []common.Event
	prepares, activates, restores int
	restoreErr, activateErr       error
	prepareErr                    error
	restoreOnceErr                error
	injectErr                     error
	restoreGate                   <-chan struct{}
	injecting, allowInject        chan struct{}
}

func (d *testDriver) Prepare(ctx context.Context, _ ownership.Token, _ bool, _ *pointer.Entry, _ func(common.Event) error, _ func(bool)) error {
	d.mu.Lock()
	d.prepares++
	d.mu.Unlock()
	select {
	case <-d.ready:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.prepareErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *testDriver) Activate(context.Context, ownership.Token) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.activates++
	return d.activateErr
}
func (d *testDriver) Restore(ctx context.Context) error {
	if d.restoreGate != nil {
		select {
		case <-d.restoreGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restores++
	if d.restores == 1 && d.restoreOnceErr != nil {
		return d.restoreOnceErr
	}
	return d.restoreErr
}
func (d *testDriver) Inject(e common.Event) error {
	if d.injecting != nil {
		select {
		case d.injecting <- struct{}{}:
		default:
		}
		<-d.allowInject
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.injectErr != nil {
		return d.injectErr
	}
	d.inputs = append(d.inputs, e)
	return nil
}
func (d *testDriver) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.inputs) }

type testPair struct {
	ctx     context.Context
	cancel  context.CancelFunc
	nodes   [2]*Session
	drivers [2]*testDriver
	devices [2]*Device
	wire    [2]chan Packet
	clock   atomic.Int64
}

func newPair(t *testing.T, ready bool) *testPair {
	t.Helper()
	p := &testPair{}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.clock.Store(time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC).UnixNano())
	ids := [2]string{"a-mac", "b-windows"}
	for i := range p.nodes {
		p.drivers[i] = &testDriver{ready: make(chan struct{})}
		if ready {
			close(p.drivers[i].ready)
		}
		p.devices[i] = NewDevice(p.drivers[i])
		p.wire[i] = make(chan Packet, 128)
	}
	for i := range p.nodes {
		node, err := New(p.ctx, Config{Local: ids[i], Remote: ids[1-i], Session: "0123456789abcdef0123456789abcdef", Device: p.devices[i], Now: p.now, Send: func(ctx context.Context, packet Packet) error {
			data, err := json.Marshal(packet)
			if err != nil {
				return err
			}
			var copy Packet
			if err = json.Unmarshal(data, &copy); err != nil {
				return err
			}
			select {
			case p.wire[1-i] <- copy:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		p.nodes[i] = node
	}
	for i := range p.nodes {
		go func() {
			for {
				select {
				case <-p.ctx.Done():
					return
				case packet := <-p.wire[i]:
					_ = p.nodes[i].Handle(packet)
				}
			}
		}()
	}
	t.Cleanup(func() {
		p.cancel()
		for _, n := range p.nodes {
			n.Close()
		}
	})
	return p
}
func (p *testPair) now() time.Time { return time.Unix(0, p.clock.Load()) }
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("等待状态超时")
		}
		time.Sleep(time.Millisecond)
	}
}
func (p *testPair) request(t *testing.T, source int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
	defer cancel()
	if err := p.nodes[source].Request(ctx, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.nodes[1-source].Snapshot().Phase == ownership.Active })
}
func (p *testPair) local(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool {
		return p.nodes[0].Snapshot().Phase == ownership.Local && p.nodes[1].Snapshot().Phase == ownership.Local
	})
}

func TestNativeReadyAndBothDirections(t *testing.T) {
	p := newPair(t, false)
	result := make(chan error, 1)
	go func() { result <- p.nodes[0].Request(p.ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[1].Snapshot().Phase == ownership.Preparing })
	if err := p.nodes[0].SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err == nil {
		t.Fatal("准备期接受输入")
	}
	close(p.drivers[0].ready)
	time.Sleep(10 * time.Millisecond)
	if p.nodes[0].Snapshot().Phase == ownership.Active {
		t.Fatal("只收到一侧就绪却已激活")
	}
	close(p.drivers[1].ready)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.nodes[1].Snapshot().Phase == ownership.Active })
	if err := p.nodes[0].SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.drivers[1].count() == 1 })
	if err := p.nodes[0].Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	p.local(t)
	p.request(t, 1)
	if err := p.nodes[1].SendInput(common.Event{Kind: common.MouseButtonDown, Code: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.drivers[0].count() == 1 })
	t.Log("双端实际准备后才激活；两种认证设备身份都能成为输入来源")
}

func TestCancelBeforeNativeReadyAndLateCallbacks(t *testing.T) {
	p := newPair(t, false)
	ctx, cancel := context.WithCancel(p.ctx)
	result := make(chan error, 1)
	go func() { result <- p.nodes[1].Request(ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[0].Snapshot().Phase == ownership.Preparing })
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.local(t)
	close(p.drivers[0].ready)
	close(p.drivers[1].ready)
	p.request(t, 0)
	old := ownership.Token{Fence: ownership.Fence{Session: p.nodes[0].config.Session, Epoch: 1}, Source: "b-windows", Target: "a-mac", RequestID: 1}
	p.nodes[1].prepared(old, nil)
	if p.nodes[0].Snapshot().Lease.Source != "a-mac" {
		t.Fatal("迟到就绪改变方向")
	}
	t.Log("准备期取消完成恢复；迟到捕获回调不能重新授权")
}

func TestRecoveryFailureSurvivesNewConnection(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 0)
	p.drivers[1].mu.Lock()
	p.drivers[1].restoreErr = errors.New("release failed")
	p.drivers[1].mu.Unlock()
	if err := p.nodes[0].Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.nodes[1].Err() != nil })
	p.nodes[1].Close()
	_, err := New(p.ctx, Config{Local: "b-windows", Remote: "a-mac", Session: "1123456789abcdef0123456789abcdef", Device: p.devices[1], Send: func(context.Context, Packet) error { return nil }})
	if !errors.Is(err, ErrRecovery) {
		t.Fatalf("新连接绕过了恢复失败: %v", err)
	}
	t.Log("恢复失败不发Restored，同一Device换Session仍被阻断")
}

func TestOldInputAndSpoofedActorRejected(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 0)
	old := *p.nodes[0].Snapshot().Lease
	event := common.Event{Kind: common.KeyDown, Code: 4, Sequence: 1}
	if err := p.nodes[1].Handle(Packet{Kind: "input", Session: old.Session, Token: &old, Event: &event}); err != nil {
		t.Fatal(err)
	}
	if err := p.nodes[1].Handle(Packet{Kind: "input", Session: old.Session, Token: &old, Event: &event}); !errors.Is(err, ownership.ErrReplay) {
		t.Fatal(err)
	}
	if err := p.nodes[0].Handle(Packet{Kind: "input", Session: old.Session, Token: &old, Event: &event}); !errors.Is(err, ownership.ErrStale) {
		t.Fatal("允许远端自报本机来源")
	}
	if err := p.nodes[0].Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	p.local(t)
	p.request(t, 1)
	if err := p.nodes[1].Handle(Packet{Kind: "input", Session: old.Session, Token: &old, Event: &event}); !errors.Is(err, ownership.ErrStale) {
		t.Fatal(err)
	}
	if p.drivers[1].count() != 1 {
		t.Fatal("重复或旧输入进入OS注入器")
	}
}

func TestOneSidedRenewalCannotKeepLease(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 1)
	p.clock.Add(int64(1100 * time.Millisecond))
	if err := p.nodes[0].Tick(); err != nil {
		t.Fatal(err)
	}
	p.clock.Add(int64(1000 * time.Millisecond))
	if err := p.nodes[0].Tick(); err != nil {
		t.Fatal(err)
	}
	p.local(t)
	if err := p.nodes[1].SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err == nil {
		t.Fatal("超时来源仍可发送")
	}
}

func TestDelayedRenewalDoesNotRestartLocalLease(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 1)
	n := p.nodes[1]
	n.mu.Lock()
	n.renewSeq = 1
	n.renewals[1] = p.now().Add(500 * time.Millisecond)
	token := *n.state.Lease
	revision := n.revision
	n.mu.Unlock()
	p.clock.Add(int64(2100 * time.Millisecond))
	if err := n.Handle(Packet{Kind: "renewed", Session: token.Session, Token: &token, Sequence: 1, Revision: revision}); err != nil {
		t.Fatal(err)
	}
	if err := n.SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err == nil {
		t.Fatal("迟到确认重启本机租期")
	}
}

func TestInjectionAndRevokeAreSerialized(t *testing.T) {
	p := newPair(t, true)
	p.drivers[0].injecting = make(chan struct{}, 1)
	p.drivers[0].allowInject = make(chan struct{})
	p.request(t, 1)
	if err := p.nodes[1].SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.drivers[0].injecting:
	case <-time.After(time.Second):
		t.Fatal("未进入注入器")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- p.nodes[0].Stop(ownership.LocalTakeover) }()
	select {
	case <-stopped:
		t.Fatal("注入未完成却已报告撤销完成")
	case <-time.After(10 * time.Millisecond):
	}
	close(p.drivers[0].allowInject)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	p.local(t)
	if p.drivers[0].count() != 1 {
		t.Fatal("注入数量异常")
	}
}

func TestPausePersistsAndDoesNotReplay(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 0)
	if err := p.nodes[1].Pause(true, true); err != nil {
		t.Fatal(err)
	}
	p.local(t)
	if !p.devices[1].Paused() {
		t.Fatal("未保留本机暂停")
	}
	if err := p.nodes[1].Pause(false, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if p.nodes[0].Snapshot().Phase != ownership.Local {
		t.Fatal("恢复开关重放旧申请")
	}
	p.request(t, 1)
}

func TestPreparingCollisionChoosesOneSource(t *testing.T) {
	p := newPair(t, false)
	high, low := make(chan error, 1), make(chan error, 1)
	go func() { high <- p.nodes[1].Request(p.ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[0].Snapshot().Phase == ownership.Preparing })
	go func() { low <- p.nodes[0].Request(p.ctx, nil) }()
	waitFor(t, func() bool {
		state := p.nodes[0].Snapshot()
		return state.Lease != nil && state.Lease.Source == "a-mac"
	})
	close(p.drivers[0].ready)
	close(p.drivers[1].ready)
	if err := <-low; err != nil {
		t.Fatal(err)
	}
	if err := <-high; err == nil {
		t.Fatal("竞争落败方被激活")
	}
	for _, node := range p.nodes {
		waitFor(t, func() bool {
			state := node.Snapshot()
			return state.Phase == ownership.Active && state.Lease.Source == "a-mac"
		})
	}
}

func TestOldSessionCallbacksCannotStopNewConnection(t *testing.T) {
	driver := &testDriver{ready: make(chan struct{})}
	close(driver.ready)
	device := NewDevice(driver)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newSession := func(id string) *Session {
		n, err := New(ctx, Config{Local: "a", Remote: "b", Session: id, Device: device, Send: func(context.Context, Packet) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	activate := func(n *Session) ownership.Token {
		done := make(chan error, 1)
		go func() { done <- n.Request(ctx, nil) }()
		waitFor(t, func() bool { return n.Snapshot().Nodes[0].Ready })
		token := *n.Snapshot().Lease
		if err := n.Handle(Packet{Kind: "ready", Session: token.Session, Token: &token}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		return token
	}
	old := newSession("0123456789abcdef0123456789abcdef")
	oldToken := activate(old)
	old.Close()
	waitFor(t, func() bool { device.mu.Lock(); defer device.mu.Unlock(); return device.owner == "" })
	current := newSession("1123456789abcdef0123456789abcdef")
	defer current.Close()
	activate(current)
	old.Close()
	if err := old.Stop(ownership.Returned); !errors.Is(err, ownership.ErrClosed) {
		t.Fatal(err)
	}
	if err := old.Pause(true, true); !errors.Is(err, ownership.ErrClosed) {
		t.Fatal(err)
	}
	current.nativeStopped(oldToken, true)
	if err := current.StopToken(oldToken, ownership.LayoutChanged); !errors.Is(err, ownership.ErrStale) {
		t.Fatal(err)
	}
	if device.Paused() {
		t.Fatal("旧会话修改新会话暂停意图")
	}
	if err := current.SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err != nil {
		t.Fatalf("旧回调撤销了新输入门: %v", err)
	}
}

func TestLocalStopRestoresWithoutNetworkReply(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 1)
	n := p.nodes[1]
	n.mu.Lock()
	n.config.Send = func(context.Context, Packet) error { return nil }
	n.mu.Unlock()
	if err := n.Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(p.ctx, time.Second)
	defer cancel()
	if err := p.devices[1].WaitRestored(ctx); err != nil {
		t.Fatalf("本机恢复依赖远端: %v", err)
	}
	if p.nodes[0].Snapshot().Phase != ownership.Active {
		t.Fatal("测试未真正阻断释放往返")
	}
	if err := n.SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err == nil {
		t.Fatal("本机停止后仍发送")
	}
}

func TestEmergencyDuringRecoveryClearsWaitingRequest(t *testing.T) {
	p := newPair(t, false)
	gate := make(chan struct{})
	defer close(gate)
	for _, driver := range p.drivers {
		driver.restoreGate = gate
	}
	high, low := make(chan error, 1), make(chan error, 1)
	go func() { high <- p.nodes[1].Request(p.ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[0].Snapshot().Lease != nil })
	old := *p.nodes[0].Snapshot().Lease
	go func() { low <- p.nodes[0].Request(p.ctx, nil) }()
	waitFor(t, func() bool {
		return p.nodes[1].Snapshot().Phase == ownership.Recovering && p.nodes[0].Snapshot().Waiting != nil
	})
	p.nodes[1].nativeStopped(old, true)
	waitFor(t, func() bool { state := p.nodes[0].Snapshot(); return state.Waiting == nil && state.Nodes[1].Paused })
	if err := <-low; err == nil {
		t.Fatal("恢复期急停后仍完成排队申请")
	}
	if err := <-high; err == nil {
		t.Fatal("原申请未撤销")
	}
}

func TestLegacyRecoveryFailureCannotBeClearedByPeerDriver(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 0)
	p.devices[0].BlockRecovery(errors.New("旧控制路径光标恢复失败"))
	p.nodes[0].Close()
	waitFor(t, func() bool {
		p.devices[0].mu.Lock()
		defer p.devices[0].mu.Unlock()
		return p.devices[0].restoring == nil
	})
	if p.devices[0].RecoveryError() == nil {
		t.Fatal("新驱动的清理误清除了旧路径恢复失败")
	}
	_, err := New(p.ctx, Config{Local: "a-mac", Remote: "b-windows", Session: "1123456789abcdef0123456789abcdef", Device: p.devices[0], Send: func(context.Context, Packet) error { return nil }})
	if !errors.Is(err, ErrRecovery) {
		t.Fatal("切换新协议绕过旧路径恢复屏障")
	}
}

func TestPrepareFailureKeepsConnectionAndCanRetry(t *testing.T) {
	for source := range 2 {
		t.Run([]string{"coordinator-source", "follower-source"}[source], func(t *testing.T) {
			p := newPair(t, true)
			p.drivers[1-source].mu.Lock()
			p.drivers[1-source].prepareErr = errors.New("目标布局不匹配")
			p.drivers[1-source].mu.Unlock()
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
			defer cancel()
			if err := p.nodes[source].Request(ctx, nil); err == nil || err.Error() != "目标布局不匹配" {
				t.Fatalf("未保留实际准备错误: %v", err)
			}
			p.local(t)
			for _, node := range p.nodes {
				if node.Err() != nil || !node.Snapshot().Connected {
					t.Fatal("可恢复的准备错误断开了连接")
				}
			}
			old := *p.nodes[source].Snapshot().Previous
			p.drivers[1-source].mu.Lock()
			p.drivers[1-source].prepareErr = nil
			p.drivers[1-source].mu.Unlock()
			p.request(t, source)
			if err := p.nodes[0].Handle(Packet{Kind: "release", Session: old.Session, Token: &old, Reason: ownership.PermissionLost, Error: "迟到的旧错误"}); err != nil {
				t.Fatal(err)
			}
			for _, node := range p.nodes {
				if node.ControlError() != nil || node.Snapshot().Phase != ownership.Active {
					t.Fatal("旧错误污染了新控制")
				}
			}
		})
	}
}

func TestInjectionFailureKeepsConnection(t *testing.T) {
	for source := range 2 {
		t.Run([]string{"coordinator-source", "follower-source"}[source], func(t *testing.T) {
			p := newPair(t, true)
			p.request(t, source)
			p.drivers[1-source].mu.Lock()
			p.drivers[1-source].injectErr = errors.New("实际注入权限失效")
			p.drivers[1-source].mu.Unlock()
			if err := p.nodes[source].SendInput(common.Event{Kind: common.KeyDown, Code: 4}); err != nil {
				t.Fatal(err)
			}
			p.local(t)
			for _, node := range p.nodes {
				if node.Err() != nil || !node.Snapshot().Connected || node.ControlError() == nil {
					t.Fatal("注入失败丢失诊断或断开连接")
				}
			}
			p.drivers[1-source].mu.Lock()
			p.drivers[1-source].injectErr = nil
			p.drivers[1-source].mu.Unlock()
			p.request(t, source)
		})
	}
}

func TestAutomaticCloseCannotClearFirstRestoreFailure(t *testing.T) {
	p := newPair(t, true)
	p.request(t, 0)
	driver := p.drivers[1]
	driver.mu.Lock()
	driver.restoreOnceErr = errors.New("首次实际释放失败")
	driver.mu.Unlock()
	if err := p.nodes[0].Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.nodes[1].Err() != nil })
	p.nodes[1].Close()
	waitFor(t, func() bool {
		driver.mu.Lock()
		count := driver.restores
		driver.mu.Unlock()
		p.devices[1].mu.Lock()
		done := p.devices[1].restoring == nil
		p.devices[1].mu.Unlock()
		return count >= 2 && done
	})
	_, err := New(p.ctx, Config{Local: "b-windows", Remote: "a-mac", Session: "1123456789abcdef0123456789abcdef", Device: p.devices[1], Send: func(context.Context, Packet) error { return nil }})
	if !errors.Is(err, ErrRecovery) {
		t.Fatal("Close的空释放结果清除了首次恢复失败")
	}
}

func TestQueuedWinnerDoesNotKeepOldControlError(t *testing.T) {
	p := newPair(t, false)
	gate := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(gate) })
	for _, driver := range p.drivers {
		driver.restoreGate = gate
	}
	high, low := make(chan error, 1), make(chan error, 1)
	go func() { high <- p.nodes[1].Request(p.ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[0].Snapshot().Lease != nil })
	old := *p.nodes[0].Snapshot().Lease
	go func() { low <- p.nodes[0].Request(p.ctx, nil) }()
	waitFor(t, func() bool { return p.nodes[0].Snapshot().Waiting != nil })
	if err := p.nodes[0].Handle(Packet{Kind: "release", Session: old.Session, Token: &old, Reason: ownership.Returned, Error: "旧代次返回错误"}); err != nil {
		t.Fatal(err)
	}
	if p.nodes[0].ControlError() == nil {
		t.Fatal("未构造旧诊断")
	}
	once.Do(func() { close(gate) })
	close(p.drivers[0].ready)
	close(p.drivers[1].ready)
	select {
	case err := <-low:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("胜者申请未完成")
	}
	if err := <-high; err == nil {
		t.Fatal("落败方仍被激活")
	}
	waitFor(t, func() bool {
		for _, node := range p.nodes {
			if node.Snapshot().Phase != ownership.Active || node.ControlError() != nil {
				return false
			}
		}
		return true
	})
}
