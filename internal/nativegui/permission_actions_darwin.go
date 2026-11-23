//go:build gui && darwin

package nativegui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"myapp/internal/config"
	darwininput "myapp/internal/platform/darwin/input"
)

func requestPlatformPermissions(role config.Role) error {
	if err := darwininput.RequestAccess(role == config.RoleController); err != nil {
		return err
	}
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: 9})
	if err == nil {
		connection.Close()
	}
	return nil
}

func resetPlatformPermissions(ctx context.Context) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	insideBundle := false
	for directory := filepath.Dir(executable); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if strings.HasSuffix(directory, ".app") {
			data, err := os.ReadFile(filepath.Join(directory, "Contents", "Info.plist"))
			if err == nil && strings.Contains(string(data), "<string>local.myapp.client</string>") {
				insideBundle = true
			}
			break
		}
	}
	if !insideBundle {
		return errors.New("请从打包的 MyApp.app 中执行权限重置；不会重置 Terminal、ChatGPT 或其他宿主程序的权限")
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/tccutil", "reset", "All", "local.myapp.client").CombinedOutput()
	if err != nil {
		return fmt.Errorf("未能重置 MyApp 权限：%s (%w)", strings.TrimSpace(string(output)), err)
	}
	return nil
}
