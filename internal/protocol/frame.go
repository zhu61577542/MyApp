package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	HeaderSize             = 24
	CurrentMajor      byte = 1
	CurrentMinor      byte = 0
	DefaultMaxPayload      = 1 << 20
)

var (
	magic              = [4]byte{'M', 'Y', 'A', 'P'}
	ErrBadMagic        = errors.New("协议魔数无效")
	ErrVersion         = errors.New("协议主版本不兼容")
	ErrPayloadTooLarge = errors.New("协议载荷超过限制")
	ErrFlags           = errors.New("协议标志无效")
	ErrReserved        = errors.New("协议保留字段必须为零")
	ErrUnknownMessage  = errors.New("未知的关键消息")
)

type MessageType byte

const (
	TypeHello MessageType = iota + 1
	TypeHeartbeat
	TypeReleaseAll
	TypeInput
	TypeClipboard
	TypeFileManifest
	TypeFileChunk
	TypeFileManifestEnd
	TypeFileComplete
	TypeFileCommit
	TypeFileCancel
)

type Flags byte

const FlagCritical Flags = 1

type Frame struct {
	Major    byte
	Minor    byte
	Type     MessageType
	Flags    Flags
	StreamID uint64
	Payload  []byte
}

func (f Frame) Encode(writer io.Writer, maxPayload uint32) error {
	if maxPayload == 0 {
		maxPayload = DefaultMaxPayload
	}
	if len(f.Payload) > int(maxPayload) {
		return ErrPayloadTooLarge
	}
	if f.Major == 0 {
		f.Major = CurrentMajor
		f.Minor = CurrentMinor
	}
	if f.Major != CurrentMajor {
		return ErrVersion
	}
	if f.Flags & ^FlagCritical != 0 {
		return ErrFlags
	}
	if !knownType(f.Type) && f.Flags&FlagCritical != 0 {
		return ErrUnknownMessage
	}
	header := make([]byte, HeaderSize)
	copy(header[:4], magic[:])
	header[4], header[5] = f.Major, f.Minor
	header[6], header[7] = byte(f.Type), byte(f.Flags)
	binary.BigEndian.PutUint64(header[8:16], f.StreamID)
	binary.BigEndian.PutUint32(header[16:20], uint32(len(f.Payload)))
	if err := writeFull(writer, header); err != nil {
		return err
	}
	return writeFull(writer, f.Payload)
}

type Decoder struct {
	MaxPayload uint32
}

func (d Decoder) Decode(reader io.Reader) (Frame, error) {
	maxPayload := d.MaxPayload
	if maxPayload == 0 {
		maxPayload = DefaultMaxPayload
	}
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Frame{}, err
	}
	if string(header[:4]) != string(magic[:]) {
		return Frame{}, ErrBadMagic
	}
	if header[4] != CurrentMajor {
		return Frame{}, fmt.Errorf("%w: %d", ErrVersion, header[4])
	}
	flags := Flags(header[7])
	if flags & ^FlagCritical != 0 {
		return Frame{}, ErrFlags
	}
	if binary.BigEndian.Uint32(header[20:24]) != 0 {
		return Frame{}, ErrReserved
	}
	length := binary.BigEndian.Uint32(header[16:20])
	if length > maxPayload {
		return Frame{}, ErrPayloadTooLarge
	}
	messageType := MessageType(header[6])
	if !knownType(messageType) && flags&FlagCritical != 0 {
		return Frame{}, ErrUnknownMessage
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Frame{}, err
	}
	return Frame{
		Major:    header[4],
		Minor:    header[5],
		Type:     messageType,
		Flags:    flags,
		StreamID: binary.BigEndian.Uint64(header[8:16]),
		Payload:  payload,
	}, nil
}

func knownType(messageType MessageType) bool {
	return messageType >= TypeHello && messageType <= TypeFileCancel
}

func writeFull(writer io.Writer, data []byte) error {
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
