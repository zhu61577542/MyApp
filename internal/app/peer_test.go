package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"myapp/internal/identity"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/pointer"
	"myapp/internal/protocol"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

type peerTestDriver struct {
	mu     sync.Mutex
	events []common.Event
}

func (d *peerTestDriver) Prepare(context.Context, ownership.Token, bool, *pointer.Entry, func(common.Event) error, func(bool)) error {
	return nil
}
func (d *peerTestDriver) Activate(context.Context, ownership.Token) error { return nil }
func (d *peerTestDriver) Restore(context.Context) error                   { return nil }
func (d *peerTestDriver) Inject(e common.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, e)
	return nil
}
func (d *peerTestDriver) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.events) }
func peerWait(t *testing.T, fn func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for !fn() {
		if time.Now().After(end) {
			t.Fatal("状态未在期限内到达")
		}
		time.Sleep(time.Millisecond)
	}
}
func peerLinks(t *testing.T) (context.Context, [2]*Link, [2]*peerTestDriver) {
	t.Helper()
	var links [2]*Link
	var drivers [2]*peerTestDriver
	ids := [2]string{"a-mac", "b-windows"}
	for i := range links {
		links[i], _ = NewSplitLink(nil, nil)
		drivers[i] = &peerTestDriver{}
		links[i].EnableDesktop(Capabilities{}, nil, nil)
		if err := links[i].EnablePeerControl(PeerOptions{LocalID: ids[i], RemoteID: ids[1-i], Device: peercontrol.NewDevice(drivers[i])}); err != nil {
			t.Fatal(err)
		}
	}
	return runDesktopLinks(t, links[0], links[1]), links, drivers
}

func TestPeerNegotiatesOnExistingChannelsAndBothDirections(t *testing.T) {
	ctx, links, drivers := peerLinks(t)
	for i := range links {
		node, err := links[i].PeerControl(ctx)
		if err != nil || node == nil {
			t.Fatalf("协商失败 %v", err)
		}
	}
	for source := range links {
		if err := links[source].RequestControl(ctx, "begin"); err != nil {
			t.Fatal(err)
		}
		if err := links[source].SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
			t.Fatal(err)
		}
		peerWait(t, func() bool { return drivers[1-source].count() == 1 })
		if err := links[source].RequestControl(ctx, "end"); err != nil {
			t.Fatal(err)
		}
		peerWait(t, func() bool {
			for _, l := range links {
				n, _ := l.PeerControl(ctx)
				if n.Snapshot().Phase != ownership.Local {
					return false
				}
			}
			return true
		})
	}
	t.Log("同一组realtime/bulk连接完成双向申请、输入和恢复往返")
}

func TestPeerFallbackToLegacyKeepsOldCapabilities(t *testing.T) {
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	a.EnableDesktop(Capabilities{}, nil, nil)
	called := make(chan struct{}, 1)
	b.EnableDesktop(Capabilities{Control: true}, nil, func(context.Context, string) error { called <- struct{}{}; return nil })
	if err := a.EnablePeerControl(PeerOptions{LocalID: "a", RemoteID: "b", Device: peercontrol.NewDevice(&peerTestDriver{})}); err != nil {
		t.Fatal(err)
	}
	ctx := runDesktopLinks(t, a, b)
	if err := a.RequestControl(ctx, "begin"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-ctx.Done():
		t.Fatal("旧控制未回退")
	}
	if node, err := a.PeerControl(ctx); node != nil || err != nil {
		t.Fatal("不支持新能力的对端被误升级")
	}
	t.Log("对端不参与提议时回退旧流程，严格旧Capabilities仍可解析")
}

func TestPeerModeRejectsUnfencedLegacyInput(t *testing.T) {
	var links [2]*Link
	drivers := [2]*peerTestDriver{{}, {}}
	disconnected := make(chan error, 2)
	ids := [2]string{"a", "b"}
	for i := range links {
		links[i], _ = NewSplitLink(&recordingInjector{}, nil)
		links[i].EnableDesktop(Capabilities{Control: true}, nil, nil)
		if err := links[i].EnablePeerControl(PeerOptions{LocalID: ids[i], RemoteID: ids[1-i], Device: peercontrol.NewDevice(drivers[i])}); err != nil {
			t.Fatal(err)
		}
		links[i].SetDisconnectHandler(func(err error) { disconnected <- err })
	}
	ctx := runDesktopLinks(t, links[0], links[1])
	for _, link := range links {
		if n, e := link.PeerControl(ctx); e != nil || n == nil {
			t.Fatal(e)
		}
	}
	var raw bytes.Buffer
	if err := (common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}).Encode(&raw); err != nil {
		t.Fatal(err)
	}
	if err := links[0].writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypeInput, Flags: protocol.FlagCritical, Payload: raw.Bytes()}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-disconnected:
		if err == nil || !strings.Contains(err.Error(), "无令牌") {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("无令牌输入未被拒绝")
	}
	if drivers[1].count() != 0 {
		t.Fatal("裸输入进入新注入器")
	}
}

func TestPeerForgedOpenNonceIsRejected(t *testing.T) {
	l, _ := NewSplitLink(nil, nil)
	if err := l.EnablePeerControl(PeerOptions{LocalID: "a", RemoteID: "b", Device: peercontrol.NewDevice(&peerTestDriver{})}); err != nil {
		t.Fatal(err)
	}
	if err := l.peer.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := l.handlePeerFrame(context.Background(), protocol.Frame{Type: protocol.TypePeerControl, Payload: []byte(`{"kind":"open","nonce":"0123456789abcdef0123456789abcdef","remote_nonce":"00000000000000000000000000000000","session":"0123456789abcdef0123456789abcdef","paused":false}`)})
	if err == nil {
		t.Fatal("未绑定本机随机数的确认被接受")
	}
}

func TestPeerMutualTLSBindsAuthenticatedIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var identities [2]identity.Identity
	var stores [2]trust.Store
	for i := range identities {
		var err error
		identities[i], err = identity.Generate([]string{"Mac", "Windows"}[i], time.Now())
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = trust.New(t.TempDir())
	}
	for i := range stores {
		if _, err := stores[i].AddNew(identities[1-i].CertDER, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connections [2][2]*tls.Conn
	for channel := range 2 {
		client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		server, err := listener.Accept()
		if err != nil {
			client.Close()
			t.Fatal(err)
		}
		connections[0][channel] = tls.Client(client, transport.ClientTLS(identities[0], identities[1].DeviceID, stores[0]))
		connections[1][channel] = tls.Server(server, transport.ServerTLS(identities[1], stores[1]))
	}
	handshakes := make(chan error, 4)
	for i := range connections {
		for _, conn := range connections[i] {
			go func() { handshakes <- conn.HandshakeContext(ctx) }()
		}
	}
	for range 4 {
		if err := <-handshakes; err != nil {
			t.Fatal(err)
		}
	}
	var links [2]*Link
	var drivers [2]*peerTestDriver
	for i := range links {
		for _, conn := range connections[i] {
			id, err := transport.PeerDeviceID(conn.ConnectionState())
			if err != nil || id != identities[1-i].DeviceID {
				t.Fatalf("TLS认证身份不符: %v", err)
			}
		}
		links[i], _ = NewSplitLink(nil, nil)
		drivers[i] = &peerTestDriver{}
		if err := links[i].EnablePeerControl(PeerOptions{LocalID: identities[i].DeviceID, RemoteID: identities[1-i].DeviceID, Device: peercontrol.NewDevice(drivers[i])}); err != nil {
			t.Fatal(err)
		}
	}
	ended := make(chan error, 2)
	for i := range links {
		go func() { ended <- links[i].RunChannels(ctx, connections[i][0], connections[i][1]) }()
	}
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-ended:
			case <-time.After(2 * time.Second):
				t.Error("TLS会话未结束")
			}
		}
	})
	for source := range links {
		if err := links[source].RequestControl(ctx, "begin"); err != nil {
			t.Fatal(err)
		}
		if err := links[source].SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
			t.Fatal(err)
		}
		peerWait(t, func() bool { return drivers[1-source].count() == 1 })
		if err := links[source].RequestControl(ctx, "end"); err != nil {
			t.Fatal(err)
		}
		peerWait(t, func() bool {
			for _, link := range links {
				if link.CurrentPeerControl().Snapshot().Phase != ownership.Local {
					return false
				}
			}
			return true
		})
	}
	t.Log("两条真实TCP/TLS通道以受信证书身份完成双向输入与恢复；未安装系统键鼠Hook")
}
