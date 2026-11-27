package ownership

import (
	"testing"
	"time"
)

func TestStageOneAcceptance(t *testing.T) {
	t.Run("01_Mac发起", func(t *testing.T) {
		m := newMachine(t)
		s, err := m.Request("mac", 1, baseTime)
		if err != nil {
			t.Fatal(err)
		}
		token := *s.Lease
		if m.AcceptInput("mac", token, 1, baseTime) == nil {
			t.Fatal("未就绪就接受输入")
		}
		m.Ready("mac", token, baseTime)
		if m.AcceptInput("mac", token, 1, baseTime) == nil {
			t.Fatal("单方就绪就接受输入")
		}
		m.Ready("windows", token, baseTime)
		if m.AcceptInput("mac", token, 1, baseTime) != nil {
			t.Fatal("合法来源被拒绝")
		}
		t.Log("通过：双方就绪后，Mac发送、Windows接收；准备期间不能输入")
	})
	t.Run("02_Windows发起", func(t *testing.T) {
		m := newMachine(t)
		token := activate(t, m, "windows", 1, baseTime)
		if m.AcceptInput("windows", token, 1, baseTime) != nil {
			t.Fatal("Windows来源被拒绝")
		}
		t.Log("通过：Windows同样可以成为来源；控制角色不取决于系统或旧主副配置")
	})
	t.Run("03_同时申请", func(t *testing.T) {
		m := newMachine(t)
		m.Request("windows", 1, baseTime)
		m.Request("mac", 1, baseTime)
		restore(t, m, baseTime)
		token := activate(t, m, "mac", 1, baseTime)
		if m.AcceptInput("windows", token, 1, baseTime) == nil {
			t.Fatal("出现两个来源")
		}
		t.Log("通过：准备期按稳定设备标识裁决；旧准备先恢复，双方确认后只有一个来源")
	})
	t.Run("04_本地接管", func(t *testing.T) {
		m := newMachine(t)
		token := activate(t, m, "mac", 1, baseTime)
		s, _ := m.Release("windows", token, LocalTakeover, baseTime)
		m.Restored("windows", s.Fence, baseTime)
		if m.AcceptInput("mac", token, 1, baseTime) == nil || m.Snapshot().Nodes[1].State != NodeLocal || m.Snapshot().Phase != Recovering {
			t.Fatal("本地收回或恢复屏障不成立")
		}
		if _, err := m.Request("windows", 1, baseTime); err == nil {
			t.Fatal("恢复未确认就接受新申请")
		}
		t.Log("通过：Windows本地恢复不等Mac网络确认；旧输入立即拒绝，新跨屏等待双方恢复")
	})
	t.Run("05_急停与恢复", func(t *testing.T) {
		m := newMachine(t)
		token := activate(t, m, "mac", 1, baseTime)
		m.Pause("mac", true, baseTime)
		m.Resume("mac", baseTime)
		s := restore(t, m, baseTime)
		if s.Lease != nil || m.AcceptInput("mac", token, 1, baseTime) == nil {
			t.Fatal("急停后旧输入复活")
		}
		t.Log("通过：急停立即失效；恢复开关不重放旧申请，需新的触边申请")
	})
	t.Run("06_断线与超时", func(t *testing.T) {
		for _, disconnected := range []bool{false, true} {
			m := newMachine(t)
			token := activate(t, m, "mac", 1, baseTime)
			now := baseTime
			if disconnected {
				m.Disconnect(now)
			} else {
				m.Renew("mac", token, 1, now.Add(time.Second))
				now = now.Add(2 * time.Second)
				m.Tick(now)
			}
			if m.AcceptInput("mac", token, 1, now) == nil {
				t.Fatal("失联后仍接受旧输入")
			}
		}
		t.Log("通过：断线或任一端不再续租，旧控制失效；不能靠迟到消息续上")
	})
	t.Run("07_迟到消息", func(t *testing.T) {
		m := newMachine(t)
		old := activate(t, m, "mac", 1, baseTime)
		recovery, _ := m.Release("mac", old, Returned, baseTime)
		restore(t, m, baseTime)
		next := activate(t, m, "windows", 1, baseTime)
		m.Release("mac", old, Returned, baseTime)
		if _, err := m.Ready("mac", old, baseTime); err == nil {
			t.Fatal("旧就绪被接受")
		}
		if _, err := m.Restored("mac", recovery.Fence, baseTime); err == nil {
			t.Fatal("旧恢复确认被接受")
		}
		if m.AcceptInput("mac", old, 99, baseTime) == nil || m.AcceptInput("windows", next, 1, baseTime) != nil {
			t.Fatal("旧消息影响新方向")
		}
		t.Log("通过：新方向生效后，旧释放无副作用，旧输入及旧确认被拒绝")
	})
	t.Run("08_取消申请", func(t *testing.T) {
		m := newMachine(t)
		m.CancelRequest("mac", 1, baseTime)
		if _, err := m.Request("mac", 1, baseTime); err == nil {
			t.Fatal("取消先到时仍重放旧申请")
		}
		token := activate(t, m, "mac", 2, baseTime)
		m.CancelRequest("mac", 1, baseTime)
		if m.AcceptInput("mac", token, 1, baseTime) != nil {
			t.Fatal("迟到取消停止了新控制")
		}
		t.Log("通过：取消可以先于申请到达；旧申请不复活，迟到取消不影响新控制")
	})
}
