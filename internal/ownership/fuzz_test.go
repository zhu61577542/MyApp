package ownership

import (
	"fmt"
	"testing"
	"time"
)

func FuzzCommandsRemainFenced(f *testing.F) {
	f.Add([]byte{0, 1, 17, 2, 18, 3, 20, 7, 23, 16, 17, 1, 19})
	f.Add([]byte{16, 0, 7, 23, 5, 6, 15, 25, 7, 23})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			data = data[:256]
		}
		m := newMachine(t)
		var requests, renews, inputs [3]uint64
		var old Token
		var epoch uint64
		var sessions uint64 = 1
		now := baseTime
		accepted := map[Token]uint64{}
		for _, value := range data {
			now = now.Add(time.Millisecond)
			index := int(value>>4) % 3
			actor := [3]string{"mac", "windows", "stranger"}[index]
			before := m.Snapshot()
			token := old
			if before.Lease != nil && value&128 == 0 {
				token = *before.Lease
				old = token
			}
			switch value & 15 {
			case 0:
				if !before.Connected && before.Phase == Local {
					sessions++
					m, _ = New(Config{Devices: [2]string{"mac", "windows"}, SessionID: fmt.Sprintf("%032x", sessions)})
					epoch = 0
				}
				requests[index]++
				m.Request(actor, requests[index], now)
			case 1:
				m.Ready(actor, token, now)
			case 2:
				renews[index]++
				m.Renew(actor, token, renews[index], now)
			case 3:
				inputs[index]++
				sequence := inputs[index]
				if err := m.AcceptInput(actor, token, sequence, now); err == nil {
					if before.Phase != Active || before.Lease == nil || token != *before.Lease || actor != token.Source || !now.Before(before.Deadline) || sequence <= accepted[token] {
						t.Fatal("接受了未授权或旧输入")
					}
					accepted[token] = sequence
				}
			case 4:
				m.Release(actor, token, Returned, now)
			case 5:
				m.Pause(actor, true, now)
			case 6:
				m.Resume(actor, now)
			case 7:
				m.Restored(actor, before.Fence, now)
			case 8:
				m.Tick(now)
			case 9:
				m.Disconnect(now)
			case 10:
				m.Pause(actor, false, time.Time{})
			case 11:
				token.Epoch++
				if m.AcceptInput(actor, token, 1, now) == nil {
					t.Fatal("接受伪造代次")
				}
			case 12:
				m.CancelRequest(actor, requests[index], now)
			case 13:
				m.Release(actor, token, LocalTakeover, now)
			case 14:
				m.Release(actor, token, PermissionLost, now)
			case 15:
				now = now.Add(3 * time.Second)
				m.Tick(now)
			}
			s := m.Snapshot()
			if s.Epoch < epoch {
				t.Fatal("控制代次倒退")
			}
			epoch = s.Epoch
			if s.Phase == Active {
				if s.Lease == nil || s.Lease.Fence != s.Fence || !s.Connected || s.Nodes[0].Paused || s.Nodes[1].Paused || !s.Nodes[0].Ready || !s.Nodes[1].Ready {
					t.Fatal("无效活动租约")
				}
				if s.Nodes[0].State == s.Nodes[1].State {
					t.Fatal("双方成为同一种控制角色")
				}
			}
			if s.Phase == Recovering && s.Lease != nil {
				t.Fatal("恢复期间仍有活动租约")
			}
			if s.Waiting != nil && (s.Phase != Recovering || !s.Connected || s.Nodes[0].Paused || s.Nodes[1].Paused) {
				t.Fatal("失效或暂停的申请仍在排队")
			}
		}
	})
}
