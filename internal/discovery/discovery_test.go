package discovery

import (
	"context"
	"net"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
)

func TestSignedAnnouncementRoundTrip(t *testing.T) {
	now := time.Now()
	deviceIdentity, err := identity.Generate("主电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	instanceID, err := NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	advertiser := Advertiser{Identity: deviceIdentity, InstanceID: instanceID, Role: config.RoleController, ControlPort: 24800, PairingEnabled: true}
	announcement, err := advertiser.Announcement(now)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := Encode(announcement)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(packet, now)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.DeviceID != deviceIdentity.DeviceID || decoded.DeviceName != "主电脑" || !decoded.PairingEnabled {
		t.Fatalf("发现内容错误: %+v", decoded)
	}
	announcement.ControlPort++
	if err := Validate(announcement, now); err == nil {
		t.Fatal("篡改的发现报文未被拒绝")
	}
	if _, err := Decode(packet, now.Add(MaxAge+time.Second)); err == nil {
		t.Fatal("过期的发现报文未被拒绝")
	}
}

func TestReceiveOverRealUDP(t *testing.T) {
	now := time.Now()
	deviceIdentity, err := identity.Generate("子电脑", now)
	if err != nil {
		t.Fatal(err)
	}
	instanceID, err := NewInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	announcement, err := (Advertiser{Identity: deviceIdentity, InstanceID: instanceID, Role: config.RoleAgent, ControlPort: 24800}).Announcement(now)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := Encode(announcement)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.WriteTo(packet, receiver.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, _, err := Receive(ctx, receiver)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != deviceIdentity.DeviceID {
		t.Fatalf("发现了错误设备: %s", got.DeviceID)
	}
}
