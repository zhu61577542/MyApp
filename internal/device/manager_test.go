package device

import (
	"errors"
	"testing"

	"myapp/internal/config"
)

func TestControllerLimitAndRecovery(t *testing.T) {
	manager, err := NewManager(Device{ID: "main", Name: "主电脑", Role: config.RoleController, Online: true}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range []Device{
		{ID: "a", Name: "子电脑 A", Role: config.RoleAgent, Online: true},
		{ID: "b", Name: "子电脑 B", Role: config.RoleAgent},
	} {
		if err := manager.AddPeer(peer); err != nil {
			t.Fatal(err)
		}
	}
	if manager.Count() != 3 {
		t.Fatalf("设备数=%d", manager.Count())
	}
	if err := manager.AddPeer(Device{ID: "c", Name: "C", Role: config.RoleAgent}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("第四台设备应被拒绝: %v", err)
	}
	if err := manager.Activate("a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetOnline("a", false); err != nil {
		t.Fatal(err)
	}
	if _, active := manager.Active(); active {
		t.Fatal("断线后仍有活动目标")
	}
}

func TestAgentCannotActivate(t *testing.T) {
	manager, err := NewManager(Device{ID: "agent", Name: "子电脑", Role: config.RoleAgent}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddPeer(Device{ID: "main", Name: "主电脑", Role: config.RoleController, Online: true}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate("main"); !errors.Is(err, ErrNotController) {
		t.Fatalf("受控电脑不应激活目标: %v", err)
	}
}
