//go:build gui

package nativegui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"myapp/internal/logging"
)

func exportLogs() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(home, "Downloads")
	if _, err := os.Stat(directory); err != nil {
		directory = filepath.Dir(logging.DefaultPath())
	}
	destination := filepath.Join(directory, fmt.Sprintf("MyApp-logs-%s.log", time.Now().Format("20060102-150405")))
	if err := logging.Export(logging.DefaultPath(), destination); err != nil {
		return "", err
	}
	return destination, nil
}
