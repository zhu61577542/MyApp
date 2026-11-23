//go:build gui

package nativegui

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"myapp/internal/config"
	"myapp/internal/discovery"
	"myapp/internal/identity"
	"myapp/internal/pairing"
)

type nearbyDevice struct {
	ID      string
	Name    string
	Role    config.Role
	Address string
	Seen    time.Time
}
type nearbyBrowser struct {
	mu          sync.Mutex
	devices     map[string]nearbyDevice
	connections []*net.UDPConn
	cancel      context.CancelFunc
	failure     string
	manager     *pairingManager
	lans        []discovery.LAN
	replied     map[string]time.Time
}

func startBrowser(parent context.Context, localID string, manager *pairingManager) *nearbyBrowser {
	ctx, cancel := context.WithCancel(parent)
	lanInterfaces, _ := discovery.LANInterfaces()
	browser := &nearbyBrowser{devices: make(map[string]nearbyDevice), cancel: cancel, manager: manager, lans: lanInterfaces, replied: make(map[string]time.Time)}
	for _, item := range lanInterfaces {
		item := item
		connection, err := discovery.OpenBrowser(&item.Interface)
		if err != nil {
			continue
		}
		browser.connections = append(browser.connections, connection)
	}
	if len(browser.connections) == 0 {
		if connection, err := discovery.OpenBrowser(nil); err == nil {
			browser.connections = append(browser.connections, connection)
		} else {
			browser.failure = "局域网发现暂不可用，请通过 IP 添加。"
		}
	}
	for _, connection := range browser.connections {
		connection := connection
		go func() {
			for {
				readCtx, done := context.WithTimeout(ctx, time.Second)
				announcement, address, query, err := discovery.ReceivePacket(readCtx, connection)
				done()
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					var networkError *net.OpError
					if errors.As(err, &networkError) && !networkError.Timeout() {
						browser.mu.Lock()
						browser.failure = "局域网发现连接中断，请重新打开此窗口或通过 IP 添加。"
						browser.mu.Unlock()
						return
					}
					continue
				}
				if announcement.DeviceID == localID || !announcement.PairingEnabled {
					continue
				}
				source, ok := address.(*net.UDPAddr)
				if !ok || source.IP == nil || !browser.onLAN(source.IP) {
					continue
				}
				browser.mu.Lock()
				if _, exists := browser.devices[announcement.DeviceID]; !exists && len(browser.devices) >= 128 {
					browser.mu.Unlock()
					continue
				}
				browser.devices[announcement.DeviceID] = nearbyDevice{ID: announcement.DeviceID, Name: announcement.DeviceName, Role: announcement.Role, Address: net.JoinHostPort(source.IP.String(), strconv.Itoa(announcement.ControlPort)), Seen: time.Now()}
				browser.mu.Unlock()
				if query {
					browser.reply(connection, source)
				}
			}
		}()
	}
	go browser.queryLoop(ctx)
	return browser
}

func (b *nearbyBrowser) reply(connection *net.UDPConn, target *net.UDPAddr) {
	if !b.onLAN(target.IP) || target.Port != discovery.DefaultPort {
		return
	}
	b.mu.Lock()
	if time.Since(b.replied[target.IP.String()]) < time.Second {
		b.mu.Unlock()
		return
	}
	if len(b.replied) >= 256 {
		for key, seen := range b.replied {
			if time.Since(seen) > 10*time.Second {
				delete(b.replied, key)
			}
		}
		if len(b.replied) >= 256 {
			b.mu.Unlock()
			return
		}
	}
	b.replied[target.IP.String()] = time.Now()
	b.mu.Unlock()
	advertiser := b.manager.currentAdvertisement()
	if advertiser == nil {
		return
	}
	announcement, err := advertiser.Announcement(time.Now())
	if err != nil {
		return
	}
	packet, err := discovery.Encode(announcement)
	if err == nil {
		connection.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
		_, _ = connection.WriteToUDP(packet, target)
	}
}

func (b *nearbyBrowser) onLAN(ip net.IP) bool {
	for _, lan := range b.lans {
		if lan.Network.Contains(ip) {
			return true
		}
	}
	return false
}

func (b *nearbyBrowser) queryLoop(ctx context.Context) {
	var targets []net.IP
	seen := make(map[string]bool)
	for _, lan := range b.lans {
		for _, target := range discovery.ProbeTargets(lan.Network, lan.Address) {
			if !seen[target.String()] {
				targets = append(targets, target)
				seen[target.String()] = true
			}
		}
	}
	offset := 0
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		advertiser := b.manager.currentAdvertisement()
		if advertiser == nil || len(b.connections) == 0 {
			continue
		}
		announcement, err := advertiser.Announcement(time.Now())
		if err != nil {
			continue
		}
		packet, err := discovery.EncodeQuery(announcement)
		if err != nil {
			continue
		}
		b.mu.Lock()
		var known []net.IP
		for _, device := range b.devices {
			if len(known) >= 16 {
				break
			}
			host, _, _ := net.SplitHostPort(device.Address)
			if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() && b.onLAN(ip) {
				known = append(known, ip)
			}
		}
		b.mu.Unlock()
		connection := b.connections[0]
		for _, target := range known {
			connection.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			_, _ = connection.WriteToUDP(packet, &net.UDPAddr{IP: target, Port: discovery.DefaultPort})
		}
		for index := 0; index < min(64, len(targets)); index++ {
			target := targets[offset%len(targets)]
			offset++
			connection.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			_, _ = connection.WriteToUDP(packet, &net.UDPAddr{IP: target, Port: discovery.DefaultPort})
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}
func (b *nearbyBrowser) stop() {
	b.cancel()
	for _, connection := range b.connections {
		connection.Close()
	}
}
func (b *nearbyBrowser) snapshot() ([]nearbyDevice, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var devices []nearbyDevice
	for id, device := range b.devices {
		if time.Since(device.Seen) > discovery.MaxAge {
			delete(b.devices, id)
			continue
		}
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, b.failure
}

func (m *pairingManager) beginAdvertising(options Options, cmd *exec.Cmd, line string) {
	parts := strings.SplitN(line, "等待配对连接:", 2)
	if len(parts) != 2 {
		return
	}
	_, portText, err := net.SplitHostPort(strings.TrimSpace(parts[1]))
	if err != nil {
		return
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return
	}
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		return
	}
	local, err := identity.Load(options.IdentityDirectory)
	if err != nil {
		return
	}
	instance, err := discovery.NewInstanceID()
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.cmd != cmd || m.advertiseCancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.advertiseCancel = cancel
	advertiser := discovery.Advertiser{Identity: local, InstanceID: instance, Role: cfg.Role, ControlPort: port, PairingEnabled: true}
	m.advertisement = &advertiser
	m.mu.Unlock()
	interfaces, _ := discovery.LANInterfaces()
	if len(interfaces) == 0 {
		m.mu.Lock()
		m.discoveryError = "未检测到可用局域网接口，请检查网络并使用 IP 添加。"
		m.mu.Unlock()
	}
	for _, lan := range interfaces {
		lan := lan
		go func() {
			for ctx.Err() == nil {
				connection, err := discovery.OpenAnnouncerOn(&lan.Interface)
				if err == nil {
					err = advertiser.Run(ctx, connection, &net.UDPAddr{IP: discovery.DefaultMulticast, Port: discovery.DefaultPort}, 2*time.Second)
					connection.Close()
				}
				if ctx.Err() != nil {
					return
				}
				m.mu.Lock()
				if m.cmd == cmd {
					m.discoveryError = "部分网络接口广播失败，将自动重试；仍可使用主动发现或 IP 添加。"
				}
				m.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}()
	}
}

func (m *pairingManager) currentAdvertisement() *discovery.Advertiser {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.mode != "listen" || m.terminated || m.advertisement == nil || time.Since(m.started) > pairing.OfferLifetime {
		return nil
	}
	copy := *m.advertisement
	return &copy
}

func defaultPairAddress(options Options) string {
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		return ":24802"
	}
	_, port, _ := net.SplitHostPort(cfg.ListenAddress)
	if port == "24802" {
		return ":24803"
	}
	return ":24802"
}
func normalizePairAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if net.ParseIP(address) != nil {
		return net.JoinHostPort(address, "24802"), nil
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return "", errors.New("请输入局域网 IP 或主机:端口，例如 192.168.1.20:24802")
	}
	return address, nil
}
