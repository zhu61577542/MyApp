package main

import (
	"context"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"testing"
)

type desktopTestInjector struct{ injected, released int }

func (i *desktopTestInjector) Inject(common.Event) error { i.injected++; return nil }
func (i *desktopTestInjector) ReleaseAll() error         { i.released++; return nil }
func (i *desktopTestInjector) Close() error              { return nil }

func TestDesktopInjectorStartsLazilyAndPauseReleases(t *testing.T) {
	s := desktop.New(config.Default("agent", config.RoleAgent), "local")
	inner := &desktopTestInjector{}
	created, notified := 0, 0
	i := &desktopInjector{state: s, peer: "peer", create: func() (common.Injector, error) { created++; return inner, nil }, onBlocked: func() error { notified++; return nil }}
	event := common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}
	if err := i.Inject(event); err == nil || created != 0 {
		t.Fatal("未确认控制时允许注入或提前创建后端")
	}
	if err := i.control(context.Background(), "begin"); err != nil {
		t.Fatal(err)
	}
	if err := i.Inject(event); err != nil || inner.injected != 1 {
		t.Fatalf("确认后未注入: %v", err)
	}
	if err := i.block(true); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Target != "" || notified != 1 || inner.released == 0 {
		t.Fatal("暂停没有释放输入并通知对端")
	}
	if err := i.Inject(event); err != nil || inner.injected != 1 {
		t.Fatal("暂停后输入仍进入后端或破坏连接")
	}
	if err := i.control(context.Background(), "begin"); err == nil {
		t.Fatal("暂停时允许重新开始")
	}
	if err := i.block(false); err != nil {
		t.Fatal(err)
	}
	if err := i.control(context.Background(), "begin"); err != nil || created != 1 {
		t.Fatalf("恢复失败或重复创建注入器: %v", err)
	}
}
