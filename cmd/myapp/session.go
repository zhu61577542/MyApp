package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"myapp/internal/transport"
)

func negotiateBulk(ctx context.Context, connection *tls.Conn, local, remote transport.SessionHello, initiator bool) error {
	return withHandshakeDeadline(ctx, connection, 5*time.Second, func() error {
		if err := connection.HandshakeContext(ctx); err != nil {
			return err
		}
		peerID, err := transport.PeerDeviceID(connection.ConnectionState())
		if err != nil {
			return err
		}
		if peerID != remote.DeviceID {
			return errors.New("批量通道与实时通道的设备身份不一致")
		}
		local.Channel = transport.ChannelBulk
		local.CreatedUnixMS = time.Now().UnixMilli()
		bulk, err := transport.ExchangeHello(connection, local, peerID, initiator, time.Now())
		if err != nil {
			return err
		}
		if !bytes.Equal(bulk.Nonce, remote.Nonce) {
			return errors.New("批量通道不属于当前会话")
		}
		return nil
	})
}

func withHandshakeDeadline(ctx context.Context, connection net.Conn, timeout time.Duration, exchange func() error) error {
	deadline := time.Now().Add(timeout)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := exchange(); err != nil {
		return err
	}
	return connection.SetDeadline(time.Time{})
}

// 任一任务结束后取消会话，并等待所有任务释放资源。
func runSession(ctx context.Context, workers ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if len(workers) == 0 {
		return nil
	}
	done := make(chan error, len(workers))
	for _, worker := range workers {
		go func() { done <- worker(ctx) }()
	}
	err := <-done
	cancel()
	for range len(workers) - 1 {
		<-done
	}
	return err
}
