package integration

import (
	"bytes"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/discovery"
	"myapp/internal/identity"
	"myapp/internal/pairing"
	"myapp/internal/protocol"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

func TestDiscoverPairTrustAndExchangeFrame(t *testing.T) {
	now := time.Now()
	controller := mustIdentity(t, "主电脑", now)
	agent := mustIdentity(t, "子电脑", now)

	instanceID, err := discovery.NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	announcement, err := (discovery.Advertiser{
		Identity: agent, InstanceID: instanceID, Role: config.RoleAgent, ControlPort: 24800, PairingEnabled: true,
	}).Announcement(now)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := discovery.Encode(announcement)
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := discovery.Decode(packet, now)
	if err != nil || discovered.DeviceID != agent.DeviceID {
		t.Fatalf("发现失败: device=%s err=%v", discovered.DeviceID, err)
	}

	controllerOffer, err := pairing.New(controller, now)
	if err != nil {
		t.Fatal(err)
	}
	agentOffer, err := pairing.New(agent, now)
	if err != nil {
		t.Fatal(err)
	}
	controllerMatch, err := controllerOffer.Match(agentOffer.Offer, now)
	if err != nil {
		t.Fatal(err)
	}
	agentMatch, err := agentOffer.Match(controllerOffer.Offer, now)
	if err != nil {
		t.Fatal(err)
	}
	if controllerMatch.Code != agentMatch.Code || !bytes.Equal(controllerMatch.Confirmation, agentMatch.Confirmation) {
		t.Fatal("双方配对确认不一致")
	}

	controllerTrust := trust.New(t.TempDir())
	agentTrust := trust.New(t.TempDir())
	if _, err := controllerTrust.AddNew(controllerMatch.RemoteCertDER, now); err != nil {
		t.Fatal(err)
	}
	if _, err := agentTrust.AddNew(agentMatch.RemoteCertDER, now); err != nil {
		t.Fatal(err)
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", transport.ServerTLS(controller, controllerTrust))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverResult := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(5 * time.Second))
		incoming, err := (protocol.Decoder{}).Decode(connection)
		if err != nil {
			serverResult <- err
			return
		}
		if incoming.Type != protocol.TypeHello || string(incoming.Payload) != agent.DeviceID {
			serverResult <- errUnexpectedFrame{incoming}
			return
		}
		serverResult <- (protocol.Frame{Type: protocol.TypeHeartbeat, StreamID: incoming.StreamID}).Encode(connection, 0)
	}()

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client, err := tls.DialWithDialer(dialer, "tcp", listener.Addr().String(), transport.ClientTLS(agent, controller.DeviceID, agentTrust))
	if err != nil {
		t.Fatal(err)
	}
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if err := (protocol.Frame{Type: protocol.TypeHello, Flags: protocol.FlagCritical, StreamID: 7, Payload: []byte(agent.DeviceID)}).Encode(client, 0); err != nil {
		t.Fatal(err)
	}
	reply, err := (protocol.Decoder{}).Decode(client)
	client.Close()
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != protocol.TypeHeartbeat || reply.StreamID != 7 {
		t.Fatalf("响应错误: %+v", reply)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

type errUnexpectedFrame struct {
	frame protocol.Frame
}

func (errUnexpectedFrame) Error() string { return "服务端收到非预期协议帧" }

func mustIdentity(t *testing.T, name string, now time.Time) identity.Identity {
	t.Helper()
	result, err := identity.Generate(name, now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
