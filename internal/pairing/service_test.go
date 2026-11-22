package pairing

import (
	"context"
	"myapp/internal/identity"
	"myapp/internal/trust"
	"net"
	"testing"
	"time"
)

func TestPairThenExchangeActualServicePorts(t *testing.T) {
	now := time.Now()
	a, _ := identity.Generate("Mac", now)
	b, _ := identity.Generate("Windows", now)
	storeA, storeB := trust.New(t.TempDir()), trust.New(t.TempDir())
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		port int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		_, _, err := Run(ctx, x, a, storeA, false, func(Match) bool { return true })
		if err != nil {
			done <- result{err: err}
			return
		}
		port, err := ExchangeService(ctx, x, a, storeA, b.DeviceID, 25100, false)
		done <- result{port, err}
	}()
	if _, _, err := Run(ctx, y, b, storeB, true, func(Match) bool { return true }); err != nil {
		t.Fatal(err)
	}
	port, err := ExchangeService(ctx, y, b, storeB, a.DeviceID, 25200, true)
	server := <-done
	if err != nil || server.err != nil || port != 25100 || server.port != 25200 {
		t.Fatalf("共享端口交换错误: %d %v, %d %v", port, err, server.port, server.err)
	}
}
