package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"myapp/internal/gui"
)

func runGUI(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("gui", flag.ContinueOnError)
	set.SetOutput(stderr)
	address := set.String("addr", "127.0.0.1:24880", "GUI 监听地址")
	configPath := set.String("config", defaultConfigPath(), "配置文件路径")
	identityDirectory := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	trustDirectory := set.String("trust-dir", defaultTrustDir(), "信任目录")
	if err := set.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "MyApp GUI 已启动: http://%s\n", *address)
	server := gui.New(gui.Options{
		Address:           *address,
		ConfigPath:        *configPath,
		IdentityDir:       *identityDirectory,
		TrustDir:          *trustDirectory,
		DefaultDeviceName: hostname(),
	})
	return server.Run(ctx)
}
