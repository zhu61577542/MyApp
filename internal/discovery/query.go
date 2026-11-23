package discovery

import (
	"context"
	"errors"
	"net"
	"time"
)

var QueryMagic = [8]byte{'M', 'Y', 'A', 'P', 'D', 'S', 'Q', '1'}

func EncodeQuery(announcement Announcement) ([]byte, error) {
	packet, err := Encode(announcement)
	if err == nil {
		copy(packet[:len(Magic)], QueryMagic[:])
	}
	return packet, err
}

func DecodePacket(packet []byte, now time.Time) (Announcement, bool, error) {
	query := len(packet) >= len(QueryMagic) && string(packet[:len(QueryMagic)]) == string(QueryMagic[:])
	if query {
		packet = append([]byte(nil), packet...)
		copy(packet[:len(Magic)], Magic[:])
	}
	announcement, err := Decode(packet, now)
	return announcement, query, err
}

func ReceivePacket(ctx context.Context, connection net.PacketConn) (Announcement, net.Addr, bool, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetReadDeadline(deadline); err != nil {
			return Announcement{}, nil, false, err
		}
	}
	buffer := make([]byte, MaxPacketSize+1)
	count, address, err := connection.ReadFrom(buffer)
	if err != nil {
		return Announcement{}, address, false, err
	}
	if count > MaxPacketSize {
		return Announcement{}, address, false, errors.New("发现数据报超过大小限制")
	}
	announcement, query, err := DecodePacket(buffer[:count], time.Now())
	return announcement, address, query, err
}
