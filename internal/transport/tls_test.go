package transport

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"myapp/internal/identity"
	"myapp/internal/trust"
)

func TestMutualTLSAcceptsTrustedDevices(t *testing.T) {
	now := time.Now()
	serverIdentity := mustIdentity(t, "主电脑", now)
	clientIdentity := mustIdentity(t, "子电脑", now)
	serverTrust := trust.New(t.TempDir())
	clientTrust := trust.New(t.TempDir())
	if _, err := serverTrust.AddNew(clientIdentity.CertDER, now); err != nil {
		t.Fatal(err)
	}
	if _, err := clientTrust.AddNew(serverIdentity.CertDER, now); err != nil {
		t.Fatal(err)
	}
	clientState, serverState, clientErr, serverErr := handshake(
		ClientTLS(clientIdentity, serverIdentity.DeviceID, clientTrust),
		ServerTLS(serverIdentity, serverTrust),
	)
	if clientErr != nil || serverErr != nil {
		t.Fatalf("握手失败: client=%v server=%v", clientErr, serverErr)
	}
	if clientState.Version != tls.VersionTLS13 || serverState.NegotiatedProtocol != ALPN {
		t.Fatalf("TLS 状态异常: client=%+v server=%+v", clientState, serverState)
	}
	if id, err := PeerDeviceID(clientState); err != nil || id != serverIdentity.DeviceID {
		t.Fatalf("客户端看到的设备错误: id=%s err=%v", id, err)
	}
	if id, err := PeerDeviceID(serverState); err != nil || id != clientIdentity.DeviceID {
		t.Fatalf("服务端看到的设备错误: id=%s err=%v", id, err)
	}
}

func TestMutualTLSRejectsUntrustedClient(t *testing.T) {
	now := time.Now()
	serverIdentity := mustIdentity(t, "主电脑", now)
	clientIdentity := mustIdentity(t, "陌生电脑", now)
	serverTrust := trust.New(t.TempDir())
	clientTrust := trust.New(t.TempDir())
	if _, err := clientTrust.AddNew(serverIdentity.CertDER, now); err != nil {
		t.Fatal(err)
	}
	_, _, clientErr, serverErr := handshake(
		ClientTLS(clientIdentity, serverIdentity.DeviceID, clientTrust),
		ServerTLS(serverIdentity, serverTrust),
	)
	if clientErr == nil && serverErr == nil {
		t.Fatal("未配对客户端完成了握手")
	}
}

func TestClientRejectsUnexpectedTrustedPeer(t *testing.T) {
	now := time.Now()
	serverIdentity := mustIdentity(t, "主电脑", now)
	clientIdentity := mustIdentity(t, "子电脑", now)
	otherIdentity := mustIdentity(t, "另一台电脑", now)
	serverTrust := trust.New(t.TempDir())
	clientTrust := trust.New(t.TempDir())
	if _, err := serverTrust.AddNew(clientIdentity.CertDER, now); err != nil {
		t.Fatal(err)
	}
	if _, err := clientTrust.AddNew(serverIdentity.CertDER, now); err != nil {
		t.Fatal(err)
	}
	_, _, clientErr, _ := handshake(
		ClientTLS(clientIdentity, otherIdentity.DeviceID, clientTrust),
		ServerTLS(serverIdentity, serverTrust),
	)
	if clientErr == nil {
		t.Fatal("客户端接受了非预期设备")
	}
}

func mustIdentity(t *testing.T, name string, now time.Time) identity.Identity {
	t.Helper()
	result, err := identity.Generate(name, now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func handshake(clientConfig, serverConfig *tls.Config) (tls.ConnectionState, tls.ConnectionState, error, error) {
	clientRaw, serverRaw := net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	clientRaw.SetDeadline(deadline)
	serverRaw.SetDeadline(deadline)
	client := tls.Client(clientRaw, clientConfig)
	server := tls.Server(serverRaw, serverConfig)
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Handshake() }()
	clientErr := client.Handshake()
	serverErr := <-serverResult
	clientState, serverState := client.ConnectionState(), server.ConnectionState()
	clientRaw.Close()
	serverRaw.Close()
	return clientState, serverState, clientErr, serverErr
}
