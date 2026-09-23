package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"myapp/internal/nativegui"
)

func runNativeGUI(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("gui", flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", defaultConfigPath(), "配置文件路径")
	identityDirectory := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	if err := set.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(stdout, "MyApp 原生 GUI 正在启动")
	return nativegui.Run(ctx, nativegui.Options{ConfigPath: *configPath, IdentityDirectory: *identityDirectory, DefaultDeviceName: hostname()})
}
