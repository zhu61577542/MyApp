package input

import (
	"sort"
	"sync"
)

type Tracker struct {
	mu      sync.Mutex
	keys    map[uint16]struct{}
	buttons map[uint16]struct{}
}

func NewTracker() *Tracker {
	return &Tracker{keys: make(map[uint16]struct{}), buttons: make(map[uint16]struct{})}
}

func (t *Tracker) Apply(event Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch event.Kind {
	case KeyDown:
		t.keys[event.Code] = struct{}{}
	case KeyUp:
		delete(t.keys, event.Code)
	case MouseButtonDown:
		t.buttons[event.Code] = struct{}{}
	case MouseButtonUp:
		delete(t.buttons, event.Code)
	case ReleaseAll:
		clear(t.keys)
		clear(t.buttons)
	}
}

func (t *Tracker) ReleaseEvents(nextSequence uint64) ([]Event, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := make([]int, 0, len(t.keys))
	for code := range t.keys {
		keys = append(keys, int(code))
	}
	sort.Ints(keys)
	buttons := make([]int, 0, len(t.buttons))
	for code := range t.buttons {
		buttons = append(buttons, int(code))
	}
	sort.Ints(buttons)
	events := make([]Event, 0, len(keys)+len(buttons))
	for _, code := range keys {
		events = append(events, Event{Kind: KeyUp, Sequence: nextSequence, Code: uint16(code)})
		nextSequence++
	}
	for _, code := range buttons {
		events = append(events, Event{Kind: MouseButtonUp, Sequence: nextSequence, Code: uint16(code)})
		nextSequence++
	}
	clear(t.keys)
	clear(t.buttons)
	return events, nextSequence
}

func (t *Tracker) Counts() (keys, buttons int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.keys), len(t.buttons)
}
