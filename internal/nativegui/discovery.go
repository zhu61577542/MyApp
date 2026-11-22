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
}

func startBrowser(parent context.Context, localID string) *nearbyBrowser {
	ctx, cancel := context.WithCancel(parent)
	browser := &nearbyBrowser{devices: make(map[string]nearbyDevice), cancel: cancel}
	interfaces, _ := net.Interfaces()
	for _, item := range interfaces {
		item := item
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagMulticast == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		connection, err := discovery.OpenBrowser(&item)
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
				announcement, address, err := discovery.Receive(readCtx, connection)
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
				if !ok || source.IP == nil {
					continue
				}
				browser.mu.Lock()
				browser.devices[announcement.DeviceID] = nearbyDevice{ID: announcement.DeviceID, Name: announcement.DeviceName, Role: announcement.Role, Address: net.JoinHostPort(source.IP.String(), strconv.Itoa(announcement.ControlPort)), Seen: time.Now()}
				browser.mu.Unlock()
			}
		}()
	}
	return browser
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
	m.mu.Unlock()
	go func() {
		connection, err := discovery.OpenAnnouncer()
		if err != nil {
			return
		}
		defer connection.Close()
		advertiser := discovery.Advertiser{Identity: local, InstanceID: instance, Role: cfg.Role, ControlPort: port, PairingEnabled: true}
		_ = advertiser.Run(ctx, connection, &net.UDPAddr{IP: discovery.DefaultMulticast, Port: discovery.DefaultPort}, 2*time.Second)
	}()
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
