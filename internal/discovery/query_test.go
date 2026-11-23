package discovery

import (
	"context"
	"myapp/internal/config"
	"myapp/internal/identity"
	"net"
	"testing"
	"time"
)

func TestActiveQueryPreservesSignatureAndExpiry(t *testing.T) {
	now := time.Now()
	local, _ := identity.Generate("peer", now)
	instance, _ := NewInstanceID()
	announcement, err := (Advertiser{Identity: local, InstanceID: instance, Role: config.RoleAgent, ControlPort: 24802, PairingEnabled: true}).Announcement(now)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := EncodeQuery(announcement)
	if err != nil {
		t.Fatal(err)
	}
	got, query, err := DecodePacket(packet, now)
	if err != nil || !query || got.DeviceID != local.DeviceID {
		t.Fatalf("查询无法验证: %v", err)
	}
	if _, err := Decode(packet, now); err == nil {
		t.Fatal("旧端不应将查询误认为普通公告")
	}
	packet[len(packet)-10] ^= 1
	if _, _, err := DecodePacket(packet, now); err == nil {
		t.Fatal("接受了损坏的签名查询")
	}
	packet, _ = EncodeQuery(announcement)
	if _, _, err := DecodePacket(packet, now.Add(MaxAge+time.Second)); err == nil {
		t.Fatal("接受了过期查询")
	}
}

func TestSignedQueryAndReplyUseSameSocket(t *testing.T) {
	now := time.Now()
	local, _ := identity.Generate("peer", now)
	instance, _ := NewInstanceID()
	announcement, err := (Advertiser{Identity: local, InstanceID: instance, Role: config.RoleAgent, ControlPort: 24802, PairingEnabled: true}).Announcement(now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	query, _ := EncodeQuery(announcement)
	if _, err := a.WriteTo(query, b.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, source, isQuery, err := ReceivePacket(ctx, b)
	if err != nil || !isQuery || source.String() != a.LocalAddr().String() {
		t.Fatalf("查询源端口未保留: %v", err)
	}
	packet, _ := Encode(announcement)
	if _, err := b.WriteTo(packet, source); err != nil {
		t.Fatal(err)
	}
	got, source, isQuery, err := ReceivePacket(ctx, a)
	if err != nil || isQuery || got.DeviceID != local.DeviceID || source.String() != b.LocalAddr().String() {
		t.Fatalf("回复未使用原发现端口: %v", err)
	}
}

func TestProbeTargetsStayWithinPrivateLocalWindow(t *testing.T) {
	_, network, _ := net.ParseCIDR("192.168.1.0/24")
	local := net.ParseIP("192.168.1.6")
	targets := ProbeTargets(network, local)
	if len(targets) != 253 {
		t.Fatalf("目标数=%d", len(targets))
	}
	for _, target := range targets {
		if !network.Contains(target) || target.Equal(local) {
			t.Fatalf("非法目标 %s", target)
		}
	}
	_, wide, _ := net.ParseCIDR("10.0.0.0/8")
	if len(ProbeTargets(wide, net.ParseIP("10.2.3.4"))) != 253 {
		t.Fatal("宽网段探测没有限制为 /24")
	}
	_, public, _ := net.ParseCIDR("198.18.0.0/30")
	if len(ProbeTargets(public, net.ParseIP("198.18.0.1"))) != 0 {
		t.Fatal("不应扫描基准测试/VPN网段")
	}
}
