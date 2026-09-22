//go:build linux && x11integration

package linuxinput

import (
	"context"
	"errors"
	"testing"
	"time"

	common "myapp/internal/input"
)

var errCaptureComplete = errors.New("捕获完成")

func TestCaptureReceivesInjectedInput(t *testing.T) {
	capturer, err := NewCapturer("")
	if err != nil {
		t.Fatal(err)
	}
	defer capturer.Close()
	injector, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		seenMove, seenKeyDown, seenKeyUp := false, false, false
		err := capturer.Capture(ctx, func(event common.Event) error {
			switch event.Kind {
			case common.MouseRelative:
				seenMove = event.X != 0 || event.Y != 0
			case common.KeyDown:
				seenKeyDown = event.Code == 4
			case common.KeyUp:
				seenKeyUp = event.Code == 4
			}
			if seenMove && seenKeyDown && seenKeyUp {
				return errCaptureComplete
			}
			return nil
		})
		result <- err
	}()
	time.Sleep(50 * time.Millisecond)
	for _, event := range []common.Event{
		{Kind: common.MouseRelative, Sequence: 1, X: 5, Y: 7},
		{Kind: common.KeyDown, Sequence: 2, Code: 4},
		{Kind: common.KeyUp, Sequence: 3, Code: 4},
	} {
		if err := injector.Inject(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-result; !errors.Is(err, errCaptureComplete) {
		t.Fatalf("捕获结果=%v", err)
	}
}
