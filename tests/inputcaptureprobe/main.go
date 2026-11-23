package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"sync"
	"time"

	"myapp/internal/input"
	"myapp/internal/platform"
)

func main() {
	readyFile := flag.String("ready-file", "", "捕获就绪通知文件")
	flag.Parse()
	result := struct {
		CapturedA           bool              `json:"captured_a"`
		CapturedMouseButton bool              `json:"captured_mouse_button"`
		CapturedMouseMotion bool              `json:"captured_mouse_motion"`
		EmergencyExit       bool              `json:"emergency_exit"`
		Diagnostics         map[string]uint64 `json:"diagnostics,omitempty"`
		Error               string            `json:"error,omitempty"`
	}{}
	capturer, err := platform.NewCapturer()
	if err == nil {
		defer capturer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var mu sync.Mutex
		pressed := make(map[uint16]bool)
		handler := func(event input.Event) error {
			mu.Lock()
			defer mu.Unlock()
			if event.Kind == input.KeyDown {
				pressed[event.Code] = true
				if event.Code == 4 {
					result.CapturedA = true
				}
				if event.Code == 41 && (pressed[224] || pressed[228]) && (pressed[226] || pressed[230]) {
					result.EmergencyExit = true
					cancel()
				}
			}
			if event.Kind == input.KeyUp {
				delete(pressed, event.Code)
			}
			if event.Kind == input.MouseButtonDown && event.Code == 1 {
				result.CapturedMouseButton = true
			}
			if event.Kind == input.MouseRelative && (event.X != 0 || event.Y != 0) {
				result.CapturedMouseMotion = true
			}
			return nil
		}
		if native, ok := capturer.(input.ReadyCapturer); ok {
			var readyErr error
			err = native.CaptureWithReady(ctx, handler, func() {
				if *readyFile != "" {
					readyErr = os.WriteFile(*readyFile, []byte("ready\n"), 0o600)
					if readyErr != nil {
						cancel()
					}
				}
			})
			err = errors.Join(err, readyErr)
		} else {
			err = errors.New("捕获器没有就绪确认接口")
		}
		mu.Lock()
		if errors.Is(err, context.Canceled) && result.EmergencyExit {
			err = nil
		}
		mu.Unlock()
		if reporter, ok := capturer.(interface{ Diagnostics() map[string]uint64 }); ok {
			result.Diagnostics = reporter.Diagnostics()
		}
	}
	if err != nil {
		result.Error = err.Error()
	}
	json.NewEncoder(os.Stdout).Encode(result)
	if err != nil || !result.CapturedA || !result.CapturedMouseButton || !result.CapturedMouseMotion || !result.EmergencyExit {
		os.Exit(1)
	}
}
