package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"myapp/internal/config"
	"myapp/internal/identity"
	"myapp/internal/pairing"
	"myapp/internal/trust"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		_, err = fmt.Fprintf(stdout, "MyApp %s commit=%s protocol=1.0\n", version, commit)
	case "config":
		err = runConfig(args[1:], stdout, stderr)
	case "identity":
		err = runIdentity(args[1:], stdout, stderr)
	case "pair":
		err = runPair(args[1:], stdin, stdout, stderr)
	case "serve":
		err = runServe(args[1:], stdout, stderr)
	case "control":
		err = runControl(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
	default:
		err = fmt.Errorf("未知命令: %s", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return 1
	}
	return 0
}

func runPair(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("pair 需要 listen 或 connect")
	}
	set := flag.NewFlagSet("pair "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	identityDirectory := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	trustDirectory := set.String("trust-dir", defaultTrustDir(), "信任目录")
	addressDefault := ":24800"
	if args[0] == "connect" {
		addressDefault = ""
	}
	address := set.String("addr", addressDefault, "监听地址或目标地址")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if *address == "" {
		return errors.New("addr 不能为空")
	}
	localIdentity, err := identity.Load(*identityDirectory)
	if err != nil {
		return err
	}
	store := trust.New(*trustDirectory)
	ctx, cancel := context.WithTimeout(context.Background(), pairing.OfferLifetime)
	defer cancel()
	approve := func(match pairing.Match) bool {
		fmt.Fprintf(stdout, "远端设备: %s\n配对码: %s\n请在两台电脑核对配对码。确认请输入 yes: ", match.RemoteDeviceName, match.Code)
		scanner := bufio.NewScanner(stdin)
		return scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes")
	}
	var connection net.Conn
	var initiator bool
	switch args[0] {
	case "listen":
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *address)
		if err != nil {
			return err
		}
		defer listener.Close()
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(pairing.OfferLifetime))
		}
		fmt.Fprintln(stdout, "等待配对连接:", listener.Addr())
		connection, err = listener.Accept()
		if err != nil {
			return err
		}
	case "connect":
		initiator = true
		connection, err = (&net.Dialer{}).DialContext(ctx, "tcp", *address)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("未知 pair 命令: %s", args[0])
	}
	defer connection.Close()
	match, record, err := pairing.Run(ctx, connection, localIdentity, store, initiator, approve)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "配对完成: %s，设备 ID %s\n", record.DeviceName, match.RemoteDeviceID)
	return err
}

func runConfig(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("config 需要 init 或 validate")
	}
	switch args[0] {
	case "init":
		set := flag.NewFlagSet("config init", flag.ContinueOnError)
		set.SetOutput(stderr)
		path := set.String("path", defaultConfigPath(), "配置文件路径")
		name := set.String("name", hostname(), "设备名称")
		role := set.String("role", string(config.RoleAgent), "controller 或 agent")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		cfg := config.Default(*name, config.Role(*role))
		if err := config.SaveNew(*path, cfg); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, *path)
		return err
	case "validate":
		set := flag.NewFlagSet("config validate", flag.ContinueOnError)
		set.SetOutput(stderr)
		path := set.String("path", defaultConfigPath(), "配置文件路径")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.Load(*path)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "配置有效: %s (%s)\n", cfg.DeviceName, cfg.Role)
		return err
	default:
		return fmt.Errorf("未知 config 命令: %s", args[0])
	}
}

func runIdentity(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("identity 需要 init 或 show")
	}
	switch args[0] {
	case "init":
		set := flag.NewFlagSet("identity init", flag.ContinueOnError)
		set.SetOutput(stderr)
		directory := set.String("dir", defaultIdentityDir(), "身份目录")
		name := set.String("name", hostname(), "设备名称")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		generated, err := identity.Generate(*name, time.Now())
		if err != nil {
			return err
		}
		if err := identity.SaveNew(*directory, generated); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, generated.DeviceID)
		return err
	case "show":
		set := flag.NewFlagSet("identity show", flag.ContinueOnError)
		set.SetOutput(stderr)
		directory := set.String("dir", defaultIdentityDir(), "身份目录")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := identity.Load(*directory)
		if err != nil {
			return err
		}
		view := struct {
			DeviceID string    `json:"device_id"`
			Name     string    `json:"name"`
			NotAfter time.Time `json:"not_after"`
		}{loaded.DeviceID, loaded.Cert.Subject.OrganizationalUnit[0], loaded.Cert.NotAfter}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(view)
	default:
		return fmt.Errorf("未知 identity 命令: %s", args[0])
	}
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "用法: myapp <version|config|identity|pair|serve|control>")
}

func defaultConfigPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(directory, "MyApp", "config.json")
}

func defaultIdentityDir() string {
	return filepath.Join(filepath.Dir(defaultConfigPath()), "identity")
}

func defaultTrustDir() string {
	return filepath.Join(filepath.Dir(defaultConfigPath()), "trust")
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "MyApp Device"
	}
	return name
}
