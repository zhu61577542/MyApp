package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = 1

type Role string

const (
	RoleController Role = "controller"
	RoleAgent      Role = "agent"
)

type Config struct {
	SchemaVersion int             `json:"schema_version"`
	DeviceName    string          `json:"device_name"`
	Role          Role            `json:"role"`
	ListenAddress string          `json:"listen_address"`
	MaxDevices    int             `json:"max_devices"`
	Peers         []Peer          `json:"peers,omitempty"`
	Switch        SwitchConfig    `json:"switch"`
	Clipboard     ClipboardConfig `json:"clipboard"`
	Files         FileConfig      `json:"files"`
	Logging       LoggingConfig   `json:"logging"`
}

type Peer struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type SwitchConfig struct {
	Mode        string `json:"mode"`
	EdgeDelayMS int    `json:"edge_delay_ms"`
}

type ClipboardConfig struct {
	TextEnabled bool `json:"text_enabled"`
}

type FileConfig struct {
	Enabled bool   `json:"enabled"`
	Cache   string `json:"cache_directory"`
}

type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

type LoggingConfig struct {
	Enabled bool     `json:"enabled"`
	Level   LogLevel `json:"level"`
}

func Default(deviceName string, role Role) Config {
	return Config{
		SchemaVersion: SchemaVersion,
		DeviceName:    deviceName,
		Role:          role,
		ListenAddress: ":24800",
		MaxDevices:    3,
		Switch: SwitchConfig{
			Mode:        "edge",
			EdgeDelayMS: 150,
		},
		Clipboard: ClipboardConfig{TextEnabled: true},
		Files:     FileConfig{Enabled: true, Cache: "cache/files"},
		Logging:   LoggingConfig{Enabled: true, Level: LogLevelInfo},
	}
}

func (c Config) Validate() error {
	var problems []error
	if c.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version 必须为 %d", SchemaVersion))
	}
	name := strings.TrimSpace(c.DeviceName)
	if name == "" || name != c.DeviceName || utf8.RuneCountInString(name) > 63 || strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		problems = append(problems, errors.New("device_name 必须是 1 到 63 个字符且不能包含首尾空白或控制字符"))
	}
	if c.Role != RoleController && c.Role != RoleAgent {
		problems = append(problems, errors.New("role 必须是 controller 或 agent"))
	}
	_, port, err := net.SplitHostPort(c.ListenAddress)
	if err != nil || port == "" || port == "0" {
		problems = append(problems, errors.New("listen_address 必须包含有效的非零端口"))
	}
	if c.MaxDevices < 2 || c.MaxDevices > 3 {
		problems = append(problems, errors.New("max_devices 必须在 2 到 3 之间，并包含本机"))
	}
	if len(c.Peers)+1 > c.MaxDevices || c.Role == RoleAgent && len(c.Peers) > 0 {
		problems = append(problems, errors.New("peers 只能配置在主电脑且总设备数不能超过 max_devices"))
	}
	seen := make(map[string]bool)
	for _, peer := range c.Peers {
		host, port, err := net.SplitHostPort(peer.Address)
		if peer.ID == "" || seen[peer.ID] || err != nil || host == "" || port == "" || port == "0" {
			problems = append(problems, errors.New("peers 必须包含不重复的设备 ID 和有效地址"))
		}
		seen[peer.ID] = true
	}
	if c.Switch.Mode != "edge" {
		problems = append(problems, errors.New("switch.mode 第一版必须是 edge"))
	}
	if c.Switch.EdgeDelayMS < 0 || c.Switch.EdgeDelayMS > 2000 {
		problems = append(problems, errors.New("switch.edge_delay_ms 必须在 0 到 2000 之间"))
	}
	if c.Files.Enabled && strings.TrimSpace(c.Files.Cache) == "" {
		problems = append(problems, errors.New("启用文件复制时 cache_directory 不能为空"))
	}
	if c.Logging.Level != LogLevelDebug && c.Logging.Level != LogLevelInfo && c.Logging.Level != LogLevelWarn && c.Logging.Level != LogLevelError {
		problems = append(problems, errors.New("logging.level 必须是 debug、info、warn 或 error"))
	}
	return errors.Join(problems...)
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if cfg.Logging.Level == "" {
		cfg.Logging = LoggingConfig{Enabled: true, Level: LogLevelInfo}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("存在多余 JSON 值")
		}
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置无效: %w", err)
	}
	return cfg, nil
}

func SaveNew(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置无效: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(cfg)
	return errors.Join(writeErr, file.Close())
}

// Save overwrites an existing configuration after validation.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置无效: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		return errors.Join(err, file.Close())
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return errors.Join(encoder.Encode(cfg), file.Close())
}
