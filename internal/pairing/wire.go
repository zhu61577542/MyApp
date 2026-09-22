package pairing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"myapp/internal/identity"
	"myapp/internal/trust"
)

const MaxWireMessage = 16 * 1024

var ErrRejected = errors.New("配对未被双方确认")

type Approval func(Match) bool

type wireConfirmation struct {
	Version  int    `json:"version"`
	Approved bool   `json:"approved"`
	Token    []byte `json:"token,omitempty"`
}

func Run(ctx context.Context, connection net.Conn, localIdentity identity.Identity, store trust.Store, initiator bool, approve Approval) (Match, trust.Record, error) {
	if approve == nil {
		return Match{}, trust.Record{}, errors.New("配对确认回调不能为空")
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return Match{}, trust.Record{}, err
		}
	}
	local, err := New(localIdentity, time.Now())
	if err != nil {
		return Match{}, trust.Record{}, err
	}
	var remote Offer
	if err := exchangeJSON(connection, local.Offer, &remote, initiator); err != nil {
		return Match{}, trust.Record{}, fmt.Errorf("交换配对信息: %w", err)
	}
	match, err := local.Match(remote, time.Now())
	if err != nil {
		return Match{}, trust.Record{}, err
	}
	approved := approve(match)
	confirmation := wireConfirmation{Version: Version, Approved: approved}
	if approved {
		confirmation.Token = match.Confirmation
	}
	var remoteConfirmation wireConfirmation
	if err := exchangeJSON(connection, confirmation, &remoteConfirmation, initiator); err != nil {
		return Match{}, trust.Record{}, fmt.Errorf("交换配对确认: %w", err)
	}
	if !approved || !remoteConfirmation.Approved || remoteConfirmation.Version != Version || !hmac.Equal(match.Confirmation, remoteConfirmation.Token) {
		return Match{}, trust.Record{}, ErrRejected
	}
	record, err := store.AddNew(match.RemoteCertDER, time.Now())
	if err != nil {
		return Match{}, trust.Record{}, fmt.Errorf("保存配对信任: %w", err)
	}
	return match, record, nil
}

func exchangeJSON(connection net.Conn, outgoing any, incoming any, initiator bool) error {
	if initiator {
		if err := writeJSON(connection, outgoing); err != nil {
			return err
		}
		return readJSON(connection, incoming)
	}
	if err := readJSON(connection, incoming); err != nil {
		return err
	}
	return writeJSON(connection, outgoing)
}

func writeJSON(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > MaxWireMessage {
		return errors.New("配对消息超过大小限制")
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, data)
}

func readJSON(reader io.Reader, value any) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 || length > MaxWireMessage {
		return errors.New("配对消息长度无效")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("配对消息包含多余数据")
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
