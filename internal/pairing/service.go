package pairing

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"myapp/internal/identity"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

// 配对确认后，通过认证连接交换实际共享端口。
func ExchangeService(ctx context.Context, raw net.Conn, local identity.Identity, store trust.Store, peerID string, port int, initiator bool) (int, error) {
	if port < 1 || port > 65535 {
		return 0, errors.New("共享服务端口无效")
	}
	deadline := time.Now().Add(5 * time.Second)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return 0, err
	}
	var connection *tls.Conn
	if initiator {
		connection = tls.Client(raw, transport.ClientTLS(local, peerID, store))
	} else {
		connection = tls.Server(raw, transport.ServerTLS(local, store))
	}
	if err := connection.HandshakeContext(ctx); err != nil {
		return 0, err
	}
	actual, err := transport.PeerDeviceID(connection.ConnectionState())
	if err != nil || actual != peerID {
		return 0, errors.New("配对后的共享信息身份不一致")
	}
	type serviceInfo struct {
		Version int `json:"version"`
		Port    int `json:"port"`
	}
	var remote serviceInfo
	if err := exchangeJSON(connection, serviceInfo{Version: 1, Port: port}, &remote, initiator); err != nil {
		return 0, err
	}
	if remote.Version != 1 || remote.Port < 1 || remote.Port > 65535 {
		return 0, errors.New("对端共享服务信息无效")
	}
	return remote.Port, nil
}
