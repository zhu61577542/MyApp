package pairing

import (
	"errors"
	"net"
	"strconv"
)

func ValidateConnectAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return errors.New("连接模式必须填写主机:端口，例如 192.168.1.20:24800")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("连接端口必须是 1 到 65535")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return errors.New("连接模式不能使用 0.0.0.0 或 ::，请填写对方电脑的局域网 IP")
	}
	return nil
}
