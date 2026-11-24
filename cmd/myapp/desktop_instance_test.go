package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"myapp/internal/instance"
)

func TestDuplicateServiceIsRejectedBeforeLoadingOtherProfile(t *testing.T) {
	guard, err := instance.Acquire("desktop-service")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	err = runDesktopService([]string{"--config", "missing-other-profile.json"}, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, instance.ErrRunning) {
		t.Fatalf("重复服务没有在加载配置前被阻止: %v", err)
	}
}
