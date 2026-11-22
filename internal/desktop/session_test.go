package desktop

import (
	"context"
	"errors"
	"myapp/internal/config"
	"testing"
	"time"
)

func TestOldBindingCannotClearNewHandler(t *testing.T) {
	s := New(config.Default("local", config.RoleController), "local")
	unbind := s.Bind(func(context.Context, Command) error { return errors.New("old") })
	called := false
	s.Bind(func(context.Context, Command) error { called = true; return nil })
	unbind()
	if err := s.Apply(context.Background(), Command{Version: Version, ID: 1, Action: "stop_control"}); err != nil || !called {
		t.Fatalf("旧会话清除了新处理器: %v", err)
	}
}

func TestPrepareStopWaitsForTaskConfirmation(t *testing.T) {
	s := New(config.Default("local", config.RoleController), "local")
	requested := make(chan struct{})
	s.Task(Task{ID: "task", State: "transferring"}, func(context.Context, Command) error { close(requested); return nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Apply(ctx, Command{Version: Version, ID: 1, Action: "prepare_stop"}) }()
	<-requested
	select {
	case err := <-done:
		t.Fatalf("收到确认前退出: %v", err)
	default:
	}
	if !s.Snapshot().Paused {
		t.Fatal("退出前没有暂停新内容")
	}
	s.Task(Task{ID: "task", State: "cancelled"}, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectClearsControlAndActiveTasks(t *testing.T) {
	s := New(config.Default("local", config.RoleController), "local")
	s.Update(func(state *Snapshot) { state.Target = "peer" })
	s.Peer(Peer{ID: "peer", Status: "connected", Control: true})
	s.Task(Task{ID: "task", State: "transferring"}, nil)
	s.Disconnected("network")
	state := s.Snapshot()
	if state.Target != "" || state.Peers[0].Control || state.Tasks[0].Active() {
		t.Fatalf("断线状态未清理: %+v", state)
	}
}
