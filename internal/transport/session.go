package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
	"myapp/internal/protocol"
)

type Channel string

const (
	ChannelControl  Channel = "control"
	ChannelRealtime Channel = "realtime"
	ChannelBulk     Channel = "bulk"
)

type SessionHello struct {
	Version       int         `json:"version"`
	DeviceID      string      `json:"device_id"`
	Role          config.Role `json:"role"`
	Channel       Channel     `json:"channel"`
	Nonce         []byte      `json:"nonce"`
	CreatedUnixMS int64       `json:"created_unix_ms"`
}

func NewSessionHello(local identity.Identity, role config.Role, channel Channel, now time.Time) (SessionHello, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return SessionHello{}, err
	}
	hello := SessionHello{Version: 1, DeviceID: local.DeviceID, Role: role, Channel: channel, Nonce: nonce, CreatedUnixMS: now.UnixMilli()}
	if err := validateHello(hello, local.DeviceID, channel, now); err != nil {
		return SessionHello{}, err
	}
	return hello, nil
}

func ExchangeHello(connection io.ReadWriter, local SessionHello, expectedRemoteID string, initiator bool, now time.Time) (SessionHello, error) {
	if err := validateHello(local, local.DeviceID, local.Channel, now); err != nil {
		return SessionHello{}, fmt.Errorf("本机会话信息无效: %w", err)
	}
	write := func() error {
		payload, err := json.Marshal(local)
		if err != nil {
			return err
		}
		return (protocol.Frame{Type: protocol.TypeHello, Flags: protocol.FlagCritical, Payload: payload}).Encode(connection, 4096)
	}
	read := func() (SessionHello, error) {
		frame, err := (protocol.Decoder{MaxPayload: 4096}).Decode(connection)
		if err != nil {
			return SessionHello{}, err
		}
		if frame.Type != protocol.TypeHello || frame.Flags&protocol.FlagCritical == 0 {
			return SessionHello{}, errors.New("会话首帧不是关键 Hello")
		}
		var remote SessionHello
		decoder := json.NewDecoder(bytes.NewReader(frame.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&remote); err != nil {
			return SessionHello{}, err
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return SessionHello{}, errors.New("会话 Hello 包含多余数据")
		}
		if err := validateHello(remote, expectedRemoteID, local.Channel, now); err != nil {
			return SessionHello{}, err
		}
		if remote.Role == local.Role {
			return SessionHello{}, errors.New("会话双方角色不能相同")
		}
		return remote, nil
	}
	if initiator {
		if err := write(); err != nil {
			return SessionHello{}, err
		}
		return read()
	}
	remote, err := read()
	if err != nil {
		return SessionHello{}, err
	}
	return remote, write()
}

func validateHello(hello SessionHello, expectedDeviceID string, expectedChannel Channel, now time.Time) error {
	if hello.Version != 1 || hello.DeviceID == "" || hello.DeviceID != expectedDeviceID {
		return errors.New("会话版本或设备 ID 无效")
	}
	if hello.Role != config.RoleController && hello.Role != config.RoleAgent {
		return errors.New("会话角色无效")
	}
	if hello.Channel != expectedChannel || hello.Channel != ChannelControl && hello.Channel != ChannelRealtime && hello.Channel != ChannelBulk {
		return errors.New("会话通道无效")
	}
	if len(hello.Nonce) != 32 {
		return errors.New("会话随机数无效")
	}
	created := time.UnixMilli(hello.CreatedUnixMS)
	if created.After(now.Add(5*time.Second)) || now.Sub(created) > 30*time.Second {
		return errors.New("会话 Hello 已过期或时间异常")
	}
	return nil
}
