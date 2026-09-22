package transport

import (
	"context"
	"errors"
	"net"
)

type DialFunc func(context.Context) (net.Conn, error)
type SessionFunc func(context.Context, net.Conn) error
type RetryNotice func(attempt int, err error)

func RunReconnect(ctx context.Context, dial DialFunc, session SessionFunc, backoff *Backoff, notice RetryNotice) error {
	if dial == nil || session == nil || backoff == nil {
		return errors.New("重连参数不能为空")
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		connection, err := dial(ctx)
		if err == nil {
			backoff.Reset()
			err = session(ctx, connection)
			connection.Close()
			if err == nil {
				return nil
			}
		}
		if notice != nil {
			notice(attempt, err)
		}
		if err := backoff.Wait(ctx); err != nil {
			return err
		}
	}
}
