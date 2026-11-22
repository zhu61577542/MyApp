//go:build gui

package nativegui

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"myapp/internal/config"
)

func pairingOutputMessage(line string) string {
	if strings.Contains(strings.ToLower(line), "connection refused") {
		return "无法连接目标设备：对方没有开启配对监听，请在对方电脑选择“监听”模式，并检查端口和防火墙。"
	}
	if strings.Contains(strings.ToLower(line), "no route to host") || strings.Contains(strings.ToLower(line), "network is unreachable") {
		return "无法连接目标设备：当前网络不可达，请检查局域网地址和网络连接。"
	}
	if strings.Contains(strings.ToLower(line), "i/o timeout") || strings.Contains(strings.ToLower(line), "timed out") {
		return "连接目标设备超时：请检查对方是否开启配对监听，以及端口是否被防火墙拦截。"
	}
	return line
}

func pairingFailureMessage(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(output, "信任已确认"):
		return "双方已确认信任，但共享地址交换未完成。请确认两端均使用新版桌面界面，并在设备详情中填写对端共享服务地址；也可移除设备后重新配对。"
	case strings.Contains(lower, "connection refused"):
		return "配对未完成：目标设备未开启监听或端口不可达。请先在对方电脑选择“监听”模式。"
	case strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		return "配对未完成：网络不可达，请检查两台电脑是否在同一网络。"
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "timed out"):
		return "配对未完成：连接超时，请检查监听状态、防火墙和端口配置。"
	default:
		return "配对未完成：请检查目标地址、监听状态、端口和防火墙；详细原因已写入日志。"
	}
}

var configMu sync.Mutex

func savePairResult(options Options, text string) error {
	parts := strings.SplitN(text, "设备 ID ", 2)
	if len(parts) != 2 {
		return errors.New("配对结果缺少设备 ID")
	}
	parts = strings.SplitN(parts[1], "，地址 ", 2)
	if len(parts) != 2 {
		return errors.New("配对结果缺少设备地址")
	}
	id := strings.TrimSpace(parts[0])
	address := strings.TrimSpace(parts[1])
	if index := strings.Index(text, "共享服务地址:"); index >= 0 {
		line := strings.SplitN(text[index+len("共享服务地址:"):], "\n", 2)[0]
		if candidate := strings.TrimSpace(line); candidate != "" {
			address = candidate
		}
	}
	configMu.Lock()
	defer configMu.Unlock()
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Role != config.RoleController {
		return errors.New("受控电脑配对成功后无需加入 peers")
	}
	for index := range cfg.Peers {
		if cfg.Peers[index].ID == id {
			cfg.Peers[index].Address = address
			return config.Save(options.ConfigPath, cfg)
		}
	}
	if len(cfg.Peers)+1 > cfg.MaxDevices {
		return fmt.Errorf("已达到最多 %d 台设备", cfg.MaxDevices)
	}
	cfg.Peers = append(cfg.Peers, config.Peer{ID: id, Address: address})
	return config.Save(options.ConfigPath, cfg)
}
