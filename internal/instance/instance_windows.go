package instance

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

func acquire(name string) (*Guard, error) {
	key := fmt.Sprintf("Local\\MyApp.%x", sha256.Sum256([]byte(name)))
	eventName, err := windows.UTF16PtrFromString(key + ".activate")
	if err != nil {
		return nil, err
	}
	event, err := windows.CreateEvent(nil, 0, 0, eventName)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	mutexName, _ := windows.UTF16PtrFromString(key + ".mutex")
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.SetEvent(event)
		_ = windows.CloseHandle(event)
		_ = windows.CloseHandle(mutex)
		return nil, ErrRunning
	}
	if err != nil {
		_ = windows.CloseHandle(event)
		return nil, err
	}
	activation := make(chan struct{}, 1)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			result, err := windows.WaitForSingleObject(event, uint32((50*time.Millisecond)/time.Millisecond))
			if err != nil {
				return
			}
			if result == windows.WAIT_OBJECT_0 {
				select {
				case <-stop:
					return
				default:
				}
				select {
				case activation <- struct{}{}:
				default:
				}
			}
		}
	}()
	return &Guard{Activations: activation, close: func() error {
		close(stop)
		_ = windows.SetEvent(event)
		<-done
		return errors.Join(windows.CloseHandle(event), windows.CloseHandle(mutex))
	}}, nil
}
