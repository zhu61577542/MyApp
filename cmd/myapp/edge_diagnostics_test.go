package main

import (
	"context"
	"errors"
	"myapp/internal/pointer"
	"testing"
	"time"
)

type unreadablePointer struct{ desktopTestPointer }

func (*unreadablePointer) Position() (pointer.Point, error) {
	return pointer.Point{}, errors.New("指针读取失败测试")
}

func TestEdgePointerFailureIsReportedInsteadOfSilentReady(t *testing.T) {
	c, ctx := testDesktopCapture(t, nil)
	ctx, cancel := context.WithCancel(ctx)
	c.geometry = &unreadablePointer{}
	done := make(chan error, 1)
	go func() { done <- c.runEdges(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for c.state.Snapshot().EdgeError == "" {
		select {
		case <-deadline.C:
			t.Fatal("静默忽略了指针读取失败")
		case <-time.After(time.Millisecond):
		}
	}
	if c.state.Snapshot().EdgeReady {
		t.Fatal("读取失败仍报告就绪")
	}
}
