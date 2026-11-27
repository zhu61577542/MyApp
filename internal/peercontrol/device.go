package peercontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

var ErrRecovery = errors.New("本机键鼠恢复尚未确认")

// Driver的恢复必须等待捕获退出，并实际释放按键和恢复光标。
type Driver interface {
	Prepare(context.Context, ownership.Token, bool, *pointer.Entry, func(common.Event) error, func(bool)) error
	Activate(context.Context, ownership.Token) error
	Restore(context.Context) error
	Inject(common.Event) error
}

// Device在重连之间保留恢复屏障和暂停意图。
type Device struct {
	mu             sync.Mutex
	driver         Driver
	owner          string
	closed         bool
	clean          bool
	paused         bool
	token          ownership.Token
	active         bool
	until          time.Time
	cancel         context.CancelFunc
	prepared       chan struct{}
	restoring      chan struct{}
	err            error
	externalErr    error
	restoreFailure error
}

func NewDevice(driver Driver) *Device { return &Device{driver: driver, clean: true} }

func (d *Device) SetPaused(paused bool) { d.setPaused(paused) }

func (d *Device) RecoveryError() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return errors.Join(ErrRecovery, d.err)
	}
	return nil
}

func (d *Device) BlockRecovery(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.externalErr = errors.Join(d.externalErr, err)
	d.err, d.clean, d.active = errors.Join(d.err, err), false, false
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *Device) WaitRestored(ctx context.Context) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		clean, err := d.clean, d.err
		d.mu.Unlock()
		if err != nil {
			return errors.Join(ErrRecovery, err)
		}
		if clean {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Device) Paused() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.paused }

func (d *Device) bind(session string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.driver == nil || d.owner != "" || !d.clean || d.err != nil {
		return ErrRecovery
	}
	d.owner, d.closed = session, false
	return nil
}

func (d *Device) prepare(ctx context.Context, token ownership.Token, source bool, entry *pointer.Entry, emit func(common.Event) error, stopped func(bool), ready func(error)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owner != token.Session || d.closed || !d.clean || d.paused || d.err != nil || d.restoring != nil {
		return ErrRecovery
	}
	ctx, d.cancel = context.WithCancel(ctx)
	d.clean, d.active, d.token = false, false, token
	d.prepared = make(chan struct{})
	done := d.prepared
	go func() {
		err := d.driver.Prepare(ctx, token, source, entry, emit, stopped)
		close(done)
		ready(err)
	}()
	return nil
}

func (d *Device) activate(ctx context.Context, token ownership.Token, until time.Time, now time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owner != token.Session || d.closed || d.token != token || d.paused || d.restoring != nil || !now.Before(until) {
		return ownership.ErrStale
	}
	select {
	case <-d.prepared:
	default:
		return ownership.ErrStale
	}
	if !d.active {
		if err := d.driver.Activate(ctx, token); err != nil {
			return err
		}
	}
	d.active, d.until = true, until
	return nil
}

func (d *Device) input(token ownership.Token, now time.Time, action func() error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.paused || !d.active || d.token != token || !now.Before(d.until) {
		return ownership.ErrStale
	}
	return action()
}

func (d *Device) revoke(session string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owner != session {
		return
	}
	d.active = false
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *Device) pauseSession(session string, paused bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owner != session || d.closed {
		return ownership.ErrStale
	}
	d.paused = paused
	if paused {
		d.active = false
		if d.cancel != nil {
			d.cancel()
		}
	}
	return nil
}

func (d *Device) setPaused(paused bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = paused
	if paused {
		d.active = false
		if d.cancel != nil {
			d.cancel()
		}
	}
}

func (d *Device) restore(session string, closed bool, restored func(error)) {
	d.mu.Lock()
	if d.owner != session {
		d.mu.Unlock()
		go restored(ownership.ErrStale)
		return
	}
	d.active = false
	d.closed = d.closed || closed
	if d.cancel != nil {
		d.cancel()
	}
	if d.restoring != nil {
		done := d.restoring
		d.mu.Unlock()
		go func() {
			<-done
			d.mu.Lock()
			err := d.err
			d.mu.Unlock()
			restored(err)
		}()
		return
	}
	done, prepared := make(chan struct{}), d.prepared
	d.clean = false
	d.restoring = done
	d.mu.Unlock()
	go func() {
		if prepared != nil {
			<-prepared
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := d.driver.Restore(ctx)
		d.mu.Lock()
		if err != nil && d.restoreFailure == nil {
			d.restoreFailure = err
		}
		if err == nil {
			err = d.restoreFailure
		}
		err = errors.Join(err, d.externalErr)
		d.err, d.clean = err, err == nil
		d.token, d.prepared, d.cancel = ownership.Token{}, nil, nil
		d.restoring = nil
		if d.closed && err == nil {
			d.owner = ""
		}
		close(done)
		d.mu.Unlock()
		restored(err)
	}()
}
