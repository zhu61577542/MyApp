package pairing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"myapp/internal/identity"
	"myapp/internal/trust"
)

func TestRunPairsBothSides(t *testing.T) {
	now := time.Now()
	first, err := identity.Generate("主电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.Generate("子电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	firstStore, secondStore := trust.New(t.TempDir()), trust.New(t.TempDir())
	firstConnection, secondConnection := net.Pipe()
	defer firstConnection.Close()
	defer secondConnection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		match Match
		err   error
	}
	serverResult := make(chan result, 1)
	go func() {
		match, _, err := Run(ctx, firstConnection, first, firstStore, false, func(Match) bool { return true })
		serverResult <- result{match, err}
	}()
	clientMatch, _, clientErr := Run(ctx, secondConnection, second, secondStore, true, func(Match) bool { return true })
	server := <-serverResult
	if clientErr != nil || server.err != nil {
		t.Fatalf("配对失败: client=%v server=%v", clientErr, server.err)
	}
	if clientMatch.Code != server.match.Code {
		t.Fatal("双方配对码不同")
	}
	if _, err := firstStore.Get(second.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.Get(first.DeviceID); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsWhenOneSideDeclines(t *testing.T) {
	now := time.Now()
	first, _ := identity.Generate("主电脑", now)
	second, _ := identity.Generate("子电脑", now)
	firstConnection, secondConnection := net.Pipe()
	defer firstConnection.Close()
	defer secondConnection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverResult := make(chan error, 1)
	go func() {
		_, _, err := Run(ctx, firstConnection, first, trust.New(t.TempDir()), false, func(Match) bool { return false })
		serverResult <- err
	}()
	_, _, clientErr := Run(ctx, secondConnection, second, trust.New(t.TempDir()), true, func(Match) bool { return true })
	serverErr := <-serverResult
	if !errors.Is(clientErr, ErrRejected) || !errors.Is(serverErr, ErrRejected) {
		t.Fatalf("拒绝结果错误: client=%v server=%v", clientErr, serverErr)
	}
}
