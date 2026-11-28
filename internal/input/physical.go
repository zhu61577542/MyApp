package input

import "context"

type PhysicalObserver interface {
	Observe(context.Context, func(Event), func()) error
	Close() error
}

type PhysicalPreserver interface{ PreservePhysical(Event) }

func (t *Tracker) PreservePhysical(event Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch event.Kind {
	case KeyDown:
		delete(t.keys, event.Code)
	case MouseButtonDown:
		delete(t.buttons, event.Code)
	}
}
