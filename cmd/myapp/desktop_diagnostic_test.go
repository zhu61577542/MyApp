package main

import (
	"context"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
	"testing"
	"time"
)

func TestKeyboardDiagnosticsDistinguishInjectedRejectedAndPermissionFailure(t *testing.T) {
	state := desktop.New(config.Default("target", config.RoleAgent), "b")
	injector := &peerNativeInjector{}
	activity := &desktopActivity{ready: true}
	d := &desktopPeerDriver{state: state, activity: func() *desktopActivity { return activity }, geometry: func() (pointer.Backend, error) { return &peerNativePointer{}, nil }, createInjector: func() (common.Injector, error) { return injector, nil }}
	token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: 1}, Source: "a", Target: "b", RequestID: 1}
	ctx := context.Background()
	if err := d.Prepare(ctx, token, false, nil, nil, func(bool) {}); err != nil {
		t.Fatal(err)
	}
	defer d.Restore(ctx)
	e := common.Event{Kind: common.KeyDown, Code: 4, Sequence: 1}
	if err := d.Inject(e); err != nil {
		t.Fatal(err)
	}
	injector.denied.Store(true)
	d.injector.nextCheck = time.Time{}
	if err := d.Inject(e); err == nil {
		t.Fatal("权限撤销未拒绝输入")
	}
	d.guard.revoked.Store(true)
	if err := d.Inject(e); err != nil {
		t.Fatal(err)
	}
	d.reportInputCounts()
	diagnostics := state.DrainDiagnostics()
	countsOnly := diagnostics[:0]
	for _, diagnostic := range diagnostics {
		if diagnostic.Event == "输入链路计数" {
			countsOnly = append(countsOnly, diagnostic)
		}
	}
	diagnostics = countsOnly
	if len(diagnostics) != 1 {
		t.Fatal("汇总数量错误")
	}
	got := diagnostics[0].Fields
	if got["keyboard_received"] != uint64(3) || got["keyboard_injected"] != uint64(1) || got["keyboard_rejected"] != uint64(1) || got["failed"] != uint64(1) {
		t.Fatal("计数无法区分注入、权限错误与接管丢弃", got)
	}
}
