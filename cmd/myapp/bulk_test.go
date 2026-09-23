package main

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

func TestBulkBindingRejectsDifferentSession(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "wrong-session"}[mismatch], func(t *testing.T) {
			now := time.Now()
			clientID, err := identity.Generate("main", now)
			if err != nil {
				t.Fatal(err)
			}
			serverID, err := identity.Generate("agent", now)
			if err != nil {
				t.Fatal(err)
			}
			clientTrust, serverTrust := trust.New(t.TempDir()), trust.New(t.TempDir())
			if _, err := clientTrust.AddNew(serverID.CertDER, now); err != nil {
				t.Fatal(err)
			}
			if _, err := serverTrust.AddNew(clientID.CertDER, now); err != nil {
				t.Fatal(err)
			}
			clientHello, err := transport.NewSessionHello(clientID, config.RoleController, transport.ChannelRealtime, now)
			if err != nil {
				t.Fatal(err)
			}
			serverHello, err := transport.NewSessionHello(serverID, config.RoleAgent, transport.ChannelRealtime, now)
			if err != nil {
				t.Fatal(err)
			}
			expected := clientHello
			if mismatch {
				expected.Nonce = append([]byte(nil), clientHello.Nonce...)
				expected.Nonce[0] ^= 1
			}
			clientRaw, serverRaw := net.Pipe()
			defer clientRaw.Close()
			defer serverRaw.Close()
			client := tls.Client(clientRaw, transport.ClientTLS(clientID, serverID.DeviceID, clientTrust))
			server := tls.Server(serverRaw, transport.ServerTLS(serverID, serverTrust))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- negotiateBulk(ctx, server, serverHello, expected, false) }()
			clientErr := negotiateBulk(ctx, client, clientHello, serverHello, true)
			serverErr := <-done
			if mismatch && serverErr == nil {
				t.Fatal("接受了另一会话的批量连接")
			}
			if !mismatch && (clientErr != nil || serverErr != nil) {
				t.Fatalf("有效绑定失败: %v %v", clientErr, serverErr)
			}
		})
	}
}
