package ownership

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

func newMachine(t testing.TB) *Machine {
	t.Helper()
	m, err := New(Config{Devices: [2]string{"windows", "mac"}, SessionID: strings.Repeat("1", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func activate(t testing.TB, m *Machine, source string, id uint64, now time.Time) Token {
	t.Helper()
	s, err := m.Request(source, id, now)
	if err != nil || s.Lease == nil {
		t.Fatalf("申请失败: %+v %v", s, err)
	}
	token := *s.Lease
	for _, actor := range []string{token.Source, token.Target} {
		s, err = m.Ready(actor, token, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Phase != Active {
		t.Fatal("双方确认后没有生效")
	}
	return token
}

func restore(t testing.TB, m *Machine, now time.Time) Snapshot {
	t.Helper()
	fence := m.Snapshot().Fence
	var s Snapshot
	for _, actor := range []string{"mac", "windows"} {
		var err error
		s, err = m.Restored(actor, fence, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestConfigValidationAndFreshSession(t *testing.T) {
	for _, config := range []Config{
		{}, {Devices: [2]string{"mac", "mac"}}, {Devices: [2]string{"mac", "invalid\n"}},
		{Devices: [2]string{"mac", "windows"}, SessionID: "old"},
		{Devices: [2]string{"mac", "windows"}, SessionID: strings.Repeat("0", 32)},
		{Devices: [2]string{"mac", "windows"}, PrepareTimeout: -1},
		{Devices: [2]string{"mac", "windows"}, LeaseTimeout: 2 * time.Minute},
		{Devices: [2]string{"mac", "windows"}, InitiallyPaused: []string{"stranger"}},
	} {
		if _, err := New(config); !errors.Is(err, ErrInvalid) {
			t.Fatalf("接受无效配置: %+v %v", config, err)
		}
	}
	a, err := New(Config{Devices: [2]string{"windows", "mac"}, InitiallyPaused: []string{"mac"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New(Config{Devices: [2]string{"mac", "windows"}})
	if a.Snapshot().Session == b.Snapshot().Session || a.Snapshot().Coordinator != "mac" || !a.Snapshot().Nodes[0].Paused {
		t.Fatal("会话标识未更新、协调者不稳定或暂停未保留")
	}
}

func TestBothReadyRequiredAndInputBoundToAuthenticatedSource(t *testing.T) {
	m := newMachine(t)
	s, _ := m.Request("windows", 1, baseTime)
	token := *s.Lease
	if s.Nodes[1].State != NodeRequesting || s.Nodes[0].State != NodePreparing {
		t.Fatal("申请状态错误")
	}
	m.Ready("windows", token, baseTime)
	if m.AcceptInput("windows", token, 1, baseTime) == nil {
		t.Fatal("目标未就绪就允许输入")
	}
	m.Ready("mac", token, baseTime)
	if !errors.Is(m.AcceptInput("mac", token, 1, baseTime), ErrNotSource) || !errors.Is(m.AcceptInput("stranger", token, 1, baseTime), ErrUnknown) {
		t.Fatal("信任了错误的输入来源")
	}
	forged := token
	forged.Target = "stranger"
	if !errors.Is(m.AcceptInput("windows", forged, 1, baseTime), ErrStale) {
		t.Fatal("令牌目标未验证")
	}
	if err := m.AcceptInput("windows", token, 1, baseTime); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.AcceptInput("windows", token, 1, baseTime), ErrReplay) || !errors.Is(m.AcceptInput("windows", token, 0, baseTime), ErrReplay) {
		t.Fatal("重复输入未拒绝")
	}
}

func TestCollisionOrderAndRecoveryBarrier(t *testing.T) {
	for _, first := range []string{"windows", "mac"} {
		t.Run(first, func(t *testing.T) {
			m := newMachine(t)
			second := "windows"
			if first == "windows" {
				second = "mac"
			}
			initial, _ := m.Request(first, 1, baseTime)
			s, err := m.Request(second, 1, baseTime)
			if first == "mac" && !errors.Is(err, ErrBusy) || first == "windows" && err != nil {
				t.Fatal(err)
			}
			if s.Phase == Recovering {
				if m.AcceptInput(first, *initial.Lease, 1, baseTime) == nil {
					t.Fatal("竞争撤销后仍接受旧输入")
				}
				if _, err := m.Ready(first, *initial.Lease, baseTime); !errors.Is(err, ErrStale) {
					t.Fatal("迟到就绪复活了旧申请")
				}
				partial, _ := m.Restored("mac", s.Fence, baseTime)
				if partial.Phase != Recovering || partial.Lease != nil || partial.Nodes[0].State != NodeLocal {
					t.Fatal("单方恢复越过了屏障")
				}
				if _, err := m.Request("windows", 2, baseTime); !errors.Is(err, ErrBusy) {
					t.Fatal("恢复未确认就允许新申请")
				}
				s, err = m.Restored("windows", s.Fence, baseTime)
				if err != nil || s.Phase != Preparing {
					t.Fatal("双方恢复后未自动准备获胜申请", err)
				}
			}
			if s.Lease.Source != "mac" {
				t.Fatal("准备期竞争没有稳定裁决")
			}
		})
	}
}

func TestLateCallbacksCannotChangeNewDirection(t *testing.T) {
	m := newMachine(t)
	old := activate(t, m, "mac", 1, baseTime)
	s, _ := m.Release("mac", old, Returned, baseTime)
	fence := s.Fence
	if _, err := m.Restored("mac", Fence{Session: "old", Epoch: fence.Epoch}, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("恢复确认未绑定会话")
	}
	restore(t, m, baseTime)
	if _, err := m.Restored("mac", fence, baseTime); err != nil {
		t.Fatal("重复当前恢复确认不幂等", err)
	}
	next := activate(t, m, "windows", 1, baseTime)
	if next.Epoch <= old.Epoch {
		t.Fatal("控制代次未递增")
	}
	m.Release("mac", old, Returned, baseTime)
	if _, err := m.Restored("windows", fence, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("旧恢复确认影响了新控制")
	}
	if _, err := m.Ready("mac", old, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("旧就绪被接受")
	}
	if m.AcceptInput("mac", old, 99, baseTime) == nil || m.AcceptInput("windows", next, 1, baseTime) != nil {
		t.Fatal("旧输入复活或新方向不可用")
	}
}

func TestLeaseRequiresFreshRenewalFromBothNodes(t *testing.T) {
	m := newMachine(t)
	token := activate(t, m, "mac", 1, baseTime)
	m.Renew("mac", token, 1, baseTime.Add(500*time.Millisecond))
	if !m.Snapshot().Deadline.Equal(baseTime.Add(2 * time.Second)) {
		t.Fatal("单方续租延长了失联目标的控制")
	}
	m.Renew("windows", token, 1, baseTime.Add(600*time.Millisecond))
	want := baseTime.Add(2500 * time.Millisecond)
	if !m.Snapshot().Deadline.Equal(want) {
		t.Fatal("双方续租期限错误")
	}
	if _, err := m.Renew("mac", token, 1, baseTime.Add(time.Second)); !errors.Is(err, ErrReplay) {
		t.Fatal("接受重复续租")
	}
	m.Ready("mac", token, baseTime.Add(1500*time.Millisecond))
	m.Request("mac", 1, baseTime.Add(1800*time.Millisecond))
	if !m.Snapshot().Deadline.Equal(want) {
		t.Fatal("重传请求或就绪延长了租期")
	}
	if _, err := m.Renew("mac", token, 2, want); !errors.Is(err, ErrStale) || m.Snapshot().Phase != Recovering {
		t.Fatal("过期后可以续租复活")
	}
	if m.AcceptInput("mac", token, 1, want) == nil {
		t.Fatal("租约失效后仍接受输入")
	}
}

func TestDelayedReadinessCannotRefreshTheOtherNode(t *testing.T) {
	m := newMachine(t)
	s, _ := m.Request("mac", 1, baseTime)
	token := *s.Lease
	m.Ready("mac", token, baseTime)
	m.Ready("mac", token, baseTime.Add(500*time.Millisecond))
	s, err := m.Ready("windows", token, baseTime.Add(time.Second))
	if err != nil || !s.Deadline.Equal(baseTime.Add(2*time.Second)) {
		t.Fatal("后到或重复的就绪刷新了先到一端的活性")
	}
	m, _ = New(Config{Devices: [2]string{"mac", "windows"}, PrepareTimeout: 2 * time.Second, LeaseTimeout: 500 * time.Millisecond})
	s, _ = m.Request("mac", 1, baseTime)
	token = *s.Lease
	m.Ready("mac", token, baseTime)
	s, err = m.Ready("windows", token, baseTime.Add(time.Second))
	if !errors.Is(err, ErrStale) || s.Phase != Recovering || s.Lease != nil {
		t.Fatal("先到一端活性过期后仍激活控制")
	}
}

func TestQueuedCollisionCancelledByPauseDisconnectOrTimeout(t *testing.T) {
	for _, action := range []string{"pause", "disconnect", "timeout"} {
		t.Run(action, func(t *testing.T) {
			m := newMachine(t)
			m.Request("windows", 1, baseTime)
			m.Request("mac", 1, baseTime)
			now := baseTime
			switch action {
			case "pause":
				m.Pause("windows", true, now)
				m.Resume("windows", now)
			case "disconnect":
				m.Disconnect(now)
			case "timeout":
				now = now.Add(2 * time.Second)
				m.Tick(now)
			}
			s := restore(t, m, now)
			if s.Phase != Local || s.Waiting != nil || s.Lease != nil {
				t.Fatal("恢复后重放了已取消申请")
			}
		})
	}
}

func TestRejectedRequestCannotReviveAfterResumeOrRecovery(t *testing.T) {
	m := newMachine(t)
	m.Pause("mac", true, baseTime)
	if _, err := m.Request("mac", 1, baseTime); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
	m.Resume("mac", baseTime)
	if _, err := m.Request("mac", 1, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("暂停时拒绝的请求在恢复后复活")
	}
	token := activate(t, m, "mac", 2, baseTime)
	if _, err := m.Request("windows", 1, baseTime); !errors.Is(err, ErrBusy) {
		t.Fatal("生效后被普通请求抢占")
	}
	m.Release("windows", token, LocalTakeover, baseTime)
	m.Resume("mac", baseTime)
	if _, err := m.Request("windows", 2, baseTime); !errors.Is(err, ErrBusy) {
		t.Fatal("恢复开关跳过了释放确认")
	}
	restore(t, m, baseTime)
	if _, err := m.Request("windows", 1, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("曾被拒绝的旧申请复活")
	}
}

func TestCancellationTombstoneAndLateCancellation(t *testing.T) {
	m := newMachine(t)
	m.CancelRequest("mac", 1, baseTime)
	if _, err := m.Request("mac", 1, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("取消先到时，迟到申请仍可重建控制")
	}
	s, _ := m.Request("mac", 2, baseTime)
	m.CancelRequest("mac", 2, baseTime.Add(-time.Hour))
	if _, err := m.Ready("mac", *s.Lease, baseTime); !errors.Is(err, ErrStale) {
		t.Fatal("取消后的迟到就绪被接受")
	}
	restore(t, m, baseTime)
	next := activate(t, m, "mac", 3, baseTime)
	m.CancelRequest("mac", 2, baseTime)
	if m.Snapshot().Phase != Active || m.AcceptInput("mac", next, 1, baseTime) != nil {
		t.Fatal("旧取消影响新控制")
	}
	m.CancelRequest("mac", 3, baseTime)
	restore(t, m, baseTime)
	m.Request("windows", 1, baseTime)
	m.Request("mac", 4, baseTime)
	m.CancelRequest("mac", 4, baseTime)
	if s := restore(t, m, baseTime); s.Phase != Local || s.Lease != nil {
		t.Fatal("取消排队申请后仍自动准备")
	}
}

func TestRecoveryBlockCancelsWaitingButOldBlockCannotStopNewLease(t *testing.T) {
	for _, reason := range []Reason{LocalTakeover, PermissionLost, LayoutChanged} {
		t.Run(string(reason), func(t *testing.T) {
			m := newMachine(t)
			initial, _ := m.Request("windows", 1, baseTime)
			s, _ := m.Request("mac", 1, baseTime)
			s.Waiting.ID = 999
			s.Previous.Source = "stranger"
			if m.Snapshot().Waiting.ID != 1 || m.Snapshot().Previous.Source != "windows" {
				t.Fatal("排队或恢复快照可修改内部状态")
			}
			m.Release("windows", *initial.Lease, reason, baseTime)
			if s := restore(t, m, baseTime); s.Phase != Local || s.Waiting != nil || s.Lease != nil {
				t.Fatal("恢复中本地阻断未取消自动申请")
			}
			next := activate(t, m, "mac", 2, baseTime)
			m.Release("windows", *initial.Lease, reason, baseTime)
			if s := m.Snapshot(); s.Phase != Active || *s.Lease != next {
				t.Fatal("旧代次阻断影响新控制")
			}
		})
	}
}

func TestStopPathsRemainSafeWithInvalidClock(t *testing.T) {
	for _, action := range []string{"emergency", "pause", "disconnect", "takeover"} {
		t.Run(action, func(t *testing.T) {
			m := newMachine(t)
			token := activate(t, m, "mac", 1, baseTime)
			past := baseTime.Add(-time.Hour)
			switch action {
			case "emergency":
				m.Pause("mac", true, past)
			case "pause":
				m.Pause("windows", false, time.Time{})
			case "disconnect":
				m.Disconnect(past)
			case "takeover":
				m.Release("windows", token, LocalTakeover, past)
			}
			if m.Snapshot().Phase != Recovering || m.AcceptInput("mac", token, 1, baseTime) == nil {
				t.Fatal("时钟异常阻止了安全收回")
			}
		})
	}
}

func TestTimeoutSessionAndSnapshotIsolation(t *testing.T) {
	m := newMachine(t)
	s, _ := m.Request("mac", 1, baseTime)
	token := *s.Lease
	s.Lease.Source = "stranger"
	s.Nodes[0].ID = "stranger"
	if m.Snapshot().Lease.Source != "mac" || m.Snapshot().Nodes[0].ID != "mac" {
		t.Fatal("快照可修改内部状态")
	}
	m.Tick(baseTime.Add(2 * time.Second))
	if _, err := m.Ready("mac", token, baseTime.Add(2*time.Second)); !errors.Is(err, ErrStale) {
		t.Fatal("准备超时后仍可确认")
	}
	restore(t, m, baseTime.Add(2*time.Second))
	next := activate(t, m, "windows", 1, baseTime.Add(2*time.Second))
	other := newMachine(t)
	other.session = strings.Repeat("2", 32)
	activate(t, other, "windows", 1, baseTime)
	if !errors.Is(other.AcceptInput("windows", next, 1, baseTime), ErrStale) {
		t.Fatal("跨会话输入被接受")
	}
	if !errors.Is(m.AcceptInput("windows", next, 1, baseTime), ErrClock) {
		t.Fatal("接受倒退时钟")
	}
}

func TestUnknownActorsDoNotPoisonClockOrState(t *testing.T) {
	m := newMachine(t)
	token := activate(t, m, "mac", 1, baseTime)
	future := baseTime.Add(time.Hour)
	calls := []func() error{
		func() error { _, e := m.Request("stranger", 1, future); return e },
		func() error { _, e := m.Ready("stranger", token, future); return e },
		func() error { _, e := m.Renew("stranger", token, 1, future); return e },
		func() error { _, e := m.Pause("stranger", true, future); return e },
		func() error { _, e := m.Resume("stranger", future); return e },
		func() error { _, e := m.Release("stranger", token, Returned, future); return e },
		func() error { _, e := m.Restored("stranger", m.Snapshot().Fence, future); return e },
	}
	for _, call := range calls {
		if !errors.Is(call(), ErrUnknown) {
			t.Fatal("未知设备操作未拒绝")
		}
	}
	if err := m.AcceptInput("mac", token, 1, baseTime); err != nil {
		t.Fatal("未知设备污染了当前状态", err)
	}
}

func TestEpochExhaustionClosesInsteadOfWrapping(t *testing.T) {
	m := newMachine(t)
	m.epoch = math.MaxUint64 - 1
	token := activate(t, m, "mac", 1, baseTime)
	s, _ := m.Release("mac", token, Returned, baseTime)
	if s.Epoch != math.MaxUint64 || s.Connected || s.Reason != Exhausted || s.Lease != nil {
		t.Fatal("代次耗尽后回绕或继续授权")
	}
	restore(t, m, baseTime)
	if _, err := m.Request("windows", 1, baseTime); !errors.Is(err, ErrClosed) {
		t.Fatal("关闭会话可以再次授权")
	}
}

func TestConcurrentRequestsAndReadinessHaveOneOwner(t *testing.T) {
	for round := 0; round < 100; round++ {
		m := newMachine(t)
		var done sync.WaitGroup
		start := make(chan struct{})
		for _, actor := range []string{"mac", "windows"} {
			done.Add(1)
			go func() { defer done.Done(); <-start; m.Request(actor, 1, baseTime) }()
		}
		close(start)
		done.Wait()
		if m.Snapshot().Phase == Recovering {
			restore(t, m, baseTime)
		}
		token := *m.Snapshot().Lease
		for _, actor := range []string{"mac", "windows"} {
			done.Add(1)
			go func() { defer done.Done(); m.Ready(actor, token, baseTime) }()
		}
		done.Wait()
		s := m.Snapshot()
		if s.Phase != Active || s.Lease.Source != "mac" || s.Nodes[0].State != NodeSending || s.Nodes[1].State != NodeReceiving {
			t.Fatalf("并发申请得到不一致来源: %+v", s)
		}
	}
}
