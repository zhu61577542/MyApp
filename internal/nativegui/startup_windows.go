//go:build gui && windows

package nativegui

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

func loginItemPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "MyApp.vbs"), nil
}
func loginItemEnabled() (bool, error) {
	name, err := loginItemPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func setLoginItem(enabled bool, options Options) error {
	name, err := loginItemPath()
	if err != nil {
		return err
	}
	if !enabled {
		err := os.Remove(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{executable, "gui", "--background", "--config", options.ConfigPath, "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory}
	for i := range args {
		args[i] = syscall.EscapeArg(args[i])
	}
	command := strings.Join(args, " ")
	data := "CreateObject(\"WScript.Shell\").Run \"" + strings.ReplaceAll(command, "\"", "\"\"") + "\", 0, False\r\n"
	units := utf16.Encode([]rune(data))
	encoded := make([]byte, 2+len(units)*2)
	encoded[0] = 0xff
	encoded[1] = 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[2+i*2:], unit)
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	return os.WriteFile(name, encoded, 0600)
}
