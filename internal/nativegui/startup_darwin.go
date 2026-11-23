//go:build gui && darwin

package nativegui

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
)

func loginItemPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "local.myapp.client.plist"), nil
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
	var arguments bytes.Buffer
	for _, arg := range []string{executable, "gui", "--background", "--config", options.ConfigPath, "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory} {
		arguments.WriteString("<string>")
		if err := xml.EscapeText(&arguments, []byte(arg)); err != nil {
			return err
		}
		arguments.WriteString("</string>")
	}
	data := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>local.myapp.client</string><key>AssociatedBundleIdentifiers</key><array><string>local.myapp.client</string></array><key>ProgramArguments</key><array>` + arguments.String() + `</array><key>RunAtLoad</key><true/></dict></plist>`
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	return os.WriteFile(name, []byte(data), 0600)
}
