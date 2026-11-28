package input

import "testing"

func TestPhysicalDownSurvivesRemoteRelease(t *testing.T) {
	tracker := NewTracker()
	for _, event := range []Event{{Kind: KeyDown, Code: 224}, {Kind: KeyDown, Code: 225}, {Kind: MouseButtonDown, Code: 1}, {Kind: MouseButtonDown, Code: 2}} {
		tracker.Apply(event)
	}
	tracker.PreservePhysical(Event{Kind: KeyDown, Code: 224})
	tracker.PreservePhysical(Event{Kind: MouseButtonDown, Code: 1})
	events, _ := tracker.ReleaseEvents(1)
	if len(events) != 2 || events[0].Kind != KeyUp || events[0].Code != 225 || events[1].Kind != MouseButtonUp || events[1].Code != 2 {
		t.Fatal("释放了实体接管时刚按下的同名键或按钮", events)
	}
}
