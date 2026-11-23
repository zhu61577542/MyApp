package discovery

import (
	"golang.org/x/net/ipv4"
	"net"
)

type LAN struct {
	Interface net.Interface
	Address   net.IP
	Network   *net.IPNet
}

func LANInterfaces() ([]LAN, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []LAN
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 || item.Flags&net.FlagMulticast == 0 || item.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addresses, _ := item.Addrs()
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok || network.IP.To4() == nil || !network.IP.IsGlobalUnicast() || network.IP.IsLinkLocalUnicast() {
				continue
			}
			result = append(result, LAN{Interface: item, Address: network.IP.To4(), Network: network})
			break
		}
	}
	return result, nil
}

// 主动探测仅覆盖当前私有网段中不超过一个 /24 的范围。
func ProbeTargets(network *net.IPNet, local net.IP) []net.IP {
	ip := local.To4()
	if network == nil || ip == nil || !ip.IsPrivate() || !network.Contains(ip) {
		return nil
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones < 0 || ones >= 31 {
		return nil
	}
	if ones < 24 {
		ones = 24
	}
	mask := net.CIDRMask(ones, 32)
	base := ip.Mask(mask)
	count := 1 << uint(32-ones)
	start := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	var result []net.IP
	for offset := 1; offset < count-1; offset++ {
		value := start + uint32(offset)
		target := net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value)).To4()
		if !target.Equal(ip) {
			result = append(result, target)
		}
	}
	return result
}

func OpenAnnouncerOn(networkInterface *net.Interface) (*net.UDPConn, error) {
	connection, err := OpenAnnouncer()
	if err != nil {
		return nil, err
	}
	packet := ipv4.NewPacketConn(connection)
	if err = packet.SetMulticastInterface(networkInterface); err == nil {
		err = packet.SetMulticastTTL(1)
	}
	if err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}
