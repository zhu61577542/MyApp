package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"myapp/internal/input"
	"myapp/internal/platform"
)

func main() {
	result := struct {
		CapturedA           bool   `json:"captured_a"`
		CapturedMouseButton bool   `json:"captured_mouse_button"`
		EmergencyExit       bool   `json:"emergency_exit"`
		Error               string `json:"error,omitempty"`
	}{}
	capturer, err := platform.NewCapturer()
	if err == nil {
		defer capturer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var mu sync.Mutex
		pressed := make(map[uint16]bool)
		err = capturer.Capture(ctx, func(event input.Event) error {
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
			return nil
		})
		mu.Lock()
		if errors.Is(err, context.Canceled) && result.EmergencyExit {
			err = nil
		}
		mu.Unlock()
	}
	if err != nil {
		result.Error = err.Error()
	}
	json.NewEncoder(os.Stdout).Encode(result)
	if err != nil || !result.CapturedA || !result.CapturedMouseButton || !result.EmergencyExit {
		os.Exit(1)
	}
}
