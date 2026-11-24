//go:build !windows

package instance

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func acquire(name string) (*Guard, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "MyApp", "instances")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))[:16]
	lock, err := os.OpenFile(filepath.Join(root, key+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(root, key+".sock")
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return nil, err
		}
		for n := 0; n < 10; n++ {
			connection, err := net.DialTimeout("unix", socket, 50*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		return nil, ErrRunning
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := os.Chmod(socket, 0600); err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, err
	}
	activation := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
			select {
			case activation <- struct{}{}:
			default:
			}
		}
	}()
	return &Guard{Activations: activation, close: func() error {
		err := listener.Close()
		<-done
		return errors.Join(err, lock.Close())
	}}, nil
}
