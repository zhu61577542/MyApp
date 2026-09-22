package transport

import (
	"net"
	"testing"
	"time"

	"myapp/internal/config"
)

func TestExchangeSessionHello(t *testing.T) {
	now := time.Now()
	controller := mustIdentity(t, "主电脑", now)
	agent := mustIdentity(t, "子电脑", now)
	controllerHello, err := NewSessionHello(controller, config.RoleController, ChannelRealtime, now)
	if err != nil {
		t.Fatal(err)
	}
	agentHello, err := NewSessionHello(agent, config.RoleAgent, ChannelRealtime, now)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	type result struct {
		hello SessionHello
		err   error
	}
	serverResult := make(chan result, 1)
	go func() {
		remote, err := ExchangeHello(server, controllerHello, agent.DeviceID, false, now)
		serverResult <- result{remote, err}
	}()
	remote, clientErr := ExchangeHello(client, agentHello, controller.DeviceID, true, now)
	serverValue := <-serverResult
	if clientErr != nil || serverValue.err != nil {
		t.Fatalf("Hello 交换失败: client=%v server=%v", clientErr, serverValue.err)
	}
	if remote.DeviceID != controller.DeviceID || serverValue.hello.DeviceID != agent.DeviceID {
		t.Fatal("Hello 远端身份错误")
	}
}

func TestExchangeRejectsWrongExpectedPeer(t *testing.T) {
	now := time.Now()
	controller := mustIdentity(t, "主电脑", now)
	agent := mustIdentity(t, "子电脑", now)
	controllerHello, _ := NewSessionHello(controller, config.RoleController, ChannelControl, now)
	agentHello, _ := NewSessionHello(agent, config.RoleAgent, ChannelControl, now)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	serverResult := make(chan error, 1)
	go func() {
		_, err := ExchangeHello(server, controllerHello, "wrong-device", false, now)
		server.Close()
		serverResult <- err
	}()
	_, clientErr := ExchangeHello(client, agentHello, controller.DeviceID, true, now)
	serverErr := <-serverResult
	if serverErr == nil {
		t.Fatal("服务端接受了错误设备")
	}
	if clientErr == nil {
		t.Fatal("远端失败后客户端不应完成 Hello")
	}
}
