package discovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
)

const (
	Version       = 1
	MaxPacketSize = 4096
	DefaultPort   = 24801
	MaxAge        = 10 * time.Second
)

var (
	Magic            = [8]byte{'M', 'Y', 'A', 'P', 'D', 'S', 'C', '1'}
	DefaultMulticast = net.IPv4(239, 255, 42, 99)
)

type Announcement struct {
	Version        int         `json:"version"`
	InstanceID     string      `json:"instance_id"`
	DeviceID       string      `json:"device_id"`
	DeviceName     string      `json:"device_name"`
	Role           config.Role `json:"role"`
	ControlPort    int         `json:"control_port"`
	PairingEnabled bool        `json:"pairing_enabled"`
	CreatedUnixMS  int64       `json:"created_unix_ms"`
	CertificateDER []byte      `json:"certificate_der"`
	Signature      []byte      `json:"signature"`
}

type Advertiser struct {
	Identity       identity.Identity
	InstanceID     string
	Role           config.Role
	ControlPort    int
	PairingEnabled bool
}

func NewInstanceID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value)), nil
}

func (a Advertiser) Announcement(now time.Time) (Announcement, error) {
	announcement := Announcement{
		Version:        Version,
		InstanceID:     a.InstanceID,
		DeviceID:       a.Identity.DeviceID,
		DeviceName:     a.Identity.Cert.Subject.OrganizationalUnit[0],
		Role:           a.Role,
		ControlPort:    a.ControlPort,
		PairingEnabled: a.PairingEnabled,
		CreatedUnixMS:  now.UnixMilli(),
		CertificateDER: append([]byte(nil), a.Identity.CertDER...),
	}
	unsigned, err := canonicalUnsigned(announcement)
	if err != nil {
		return Announcement{}, err
	}
	announcement.Signature = ed25519.Sign(a.Identity.PrivateKey, unsigned)
	if err := Validate(announcement, now); err != nil {
		return Announcement{}, err
	}
	return announcement, nil
}

func Encode(announcement Announcement) ([]byte, error) {
	data, err := json.Marshal(announcement)
	if err != nil {
		return nil, err
	}
	if len(data)+len(Magic) > MaxPacketSize {
		return nil, errors.New("发现报文超过大小限制")
	}
	packet := make([]byte, 0, len(Magic)+len(data))
	packet = append(packet, Magic[:]...)
	packet = append(packet, data...)
	return packet, nil
}

func Decode(packet []byte, now time.Time) (Announcement, error) {
	if len(packet) <= len(Magic) || len(packet) > MaxPacketSize || string(packet[:len(Magic)]) != string(Magic[:]) {
		return Announcement{}, errors.New("发现报文格式无效")
	}
	decoder := json.NewDecoder(strings.NewReader(string(packet[len(Magic):])))
	decoder.DisallowUnknownFields()
	var announcement Announcement
	if err := decoder.Decode(&announcement); err != nil {
		return Announcement{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Announcement{}, errors.New("发现报文包含多余数据")
	}
	if err := Validate(announcement, now); err != nil {
		return Announcement{}, err
	}
	return announcement, nil
}

func Validate(announcement Announcement, now time.Time) error {
	if announcement.Version != Version {
		return errors.New("发现协议版本不兼容")
	}
	if len(announcement.InstanceID) != 26 || strings.IndexFunc(announcement.InstanceID, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", r)
	}) >= 0 || announcement.ControlPort < 1 || announcement.ControlPort > 65535 {
		return errors.New("发现实例或端口无效")
	}
	if announcement.Role != config.RoleController && announcement.Role != config.RoleAgent {
		return errors.New("发现设备角色无效")
	}
	created := time.UnixMilli(announcement.CreatedUnixMS)
	if created.After(now.Add(2*time.Second)) || now.Sub(created) > MaxAge {
		return errors.New("发现报文已过期或时间异常")
	}
	deviceID, cert, err := identity.ValidateCertificate(announcement.CertificateDER, now)
	if err != nil {
		return err
	}
	if deviceID != announcement.DeviceID || cert.Subject.OrganizationalUnit[0] != announcement.DeviceName {
		return errors.New("发现设备信息与证书不一致")
	}
	unsigned, err := canonicalUnsigned(announcement)
	if err != nil {
		return err
	}
	publicKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || !ed25519.Verify(publicKey, unsigned, announcement.Signature) {
		return errors.New("发现报文签名无效")
	}
	return nil
}

func (a Advertiser) Run(ctx context.Context, connection net.PacketConn, target net.Addr, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("发现广播间隔必须大于零")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		announcement, err := a.Announcement(time.Now())
		if err != nil {
			return err
		}
		packet, err := Encode(announcement)
		if err != nil {
			return err
		}
		if _, err := connection.WriteTo(packet, target); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func Receive(ctx context.Context, connection net.PacketConn) (Announcement, net.Addr, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetReadDeadline(deadline); err != nil {
			return Announcement{}, nil, err
		}
	}
	buffer := make([]byte, MaxPacketSize+1)
	read, address, err := connection.ReadFrom(buffer)
	if err != nil {
		if ctx.Err() != nil {
			return Announcement{}, nil, ctx.Err()
		}
		return Announcement{}, nil, err
	}
	if read > MaxPacketSize {
		return Announcement{}, address, errors.New("发现数据报超过大小限制")
	}
	announcement, err := Decode(buffer[:read], time.Now())
	if err != nil {
		return Announcement{}, address, fmt.Errorf("解析来自 %s 的发现报文: %w", address, err)
	}
	return announcement, address, nil
}

func OpenBrowser(networkInterface *net.Interface) (*net.UDPConn, error) {
	return net.ListenMulticastUDP("udp4", networkInterface, &net.UDPAddr{IP: DefaultMulticast, Port: DefaultPort})
}

func OpenAnnouncer() (*net.UDPConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
}

func canonicalUnsigned(announcement Announcement) ([]byte, error) {
	announcement.Signature = nil
	return json.Marshal(announcement)
}
