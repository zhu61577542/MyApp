package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"myapp/internal/config"
	"myapp/internal/discovery"
	"myapp/internal/identity"
)

func run() error {
	local := flag.String("local", "", "本机 LAN IPv4")
	peer := flag.String("peer", "", "对端 LAN IPv4")
	duration := flag.Duration("duration", 20*time.Second, "检查时长，最多 45 秒")
	flag.Parse()
	localIP, peerIP := net.ParseIP(*local), net.ParseIP(*peer)
	if localIP == nil || peerIP == nil || !localIP.IsPrivate() || !peerIP.IsPrivate() || *duration <= 0 || *duration > 45*time.Second {
		return errors.New("需要有效的私网地址和受限检查时长")
	}
	lans, err := discovery.LANInterfaces()
	if err != nil {
		return err
	}
	var iface *net.Interface
	for _, lan := range lans {
		if lan.Address.Equal(localIP) && lan.Network.Contains(peerIP) {
			item := lan.Interface
			iface = &item
			break
		}
	}
	if iface == nil {
		return errors.New("指定地址不属于同一 LAN")
	}
	connection, err := discovery.OpenBrowser(iface)
	if err != nil {
		return err
	}
	defer connection.Close()
	id, err := identity.Generate("MyApp signed discovery probe", time.Now())
	if err != nil {
		return err
	}
	instance, err := discovery.NewInstanceID()
	if err != nil {
		return err
	}
	advertiser := discovery.Advertiser{Identity: id, InstanceID: instance, Role: config.RoleController, ControlPort: 24800, PairingEnabled: false}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var mu sync.Mutex
	result := struct {
		Sent       int `json:"sent"`
		Queries    int `json:"queries"`
		Replies    int `json:"replies"`
		Rejected   int `json:"rejected"`
		SendErrors int `json:"send_errors"`
	}{}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			announcement, e := advertiser.Announcement(time.Now())
			var packet []byte
			if e == nil {
				packet, e = discovery.EncodeQuery(announcement)
			}
			if e == nil {
				connection.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
				_, e = connection.WriteToUDP(packet, &net.UDPAddr{IP: peerIP, Port: discovery.DefaultPort})
			}
			mu.Lock()
			if e == nil {
				result.Sent++
			} else {
				result.SendErrors++
			}
			mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	for ctx.Err() == nil {
		read, done := context.WithTimeout(ctx, 250*time.Millisecond)
		announcement, source, query, e := discovery.ReceivePacket(read, connection)
		done()
		if e != nil {
			var ne net.Error
			if !errors.As(e, &ne) || !ne.Timeout() {
				mu.Lock()
				result.Rejected++
				mu.Unlock()
			}
			continue
		}
		address, ok := source.(*net.UDPAddr)
		if !ok || !address.IP.Equal(peerIP) || address.Port != discovery.DefaultPort || announcement.DeviceID == id.DeviceID {
			continue
		}
		mu.Lock()
		if query {
			result.Queries++
		} else {
			result.Replies++
		}
		mu.Unlock()
		if query {
			response, e := advertiser.Announcement(time.Now())
			if e != nil {
				return e
			}
			packet, e := discovery.Encode(response)
			if e != nil {
				return e
			}
			connection.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			if _, e = connection.WriteToUDP(packet, address); e != nil {
				return e
			}
		}
	}
	<-ended
	mu.Lock()
	defer mu.Unlock()
	json.NewEncoder(os.Stdout).Encode(result)
	if result.Queries == 0 || result.Replies == 0 || result.SendErrors != 0 {
		return errors.New("签名发现往返未通过")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
