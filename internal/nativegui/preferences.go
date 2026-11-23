//go:build gui

package nativegui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type preferences struct {
	Appearance               string `json:"appearance"`
	OnboardingDone           bool   `json:"onboarding_done"`
	CloseHintShown           bool   `json:"close_hint_shown"`
	ConnectAutomatically     bool   `json:"connect_automatically"`
	PermissionRequestVersion int    `json:"permission_request_version,omitempty"`
}

func preferencesPath(options Options) string {
	return filepath.Join(filepath.Dir(options.ConfigPath), "desktop.json")
}
func loadPreferences(options Options) (preferences, error) {
	value := preferences{Appearance: "system", ConnectAutomatically: true}
	data, err := os.ReadFile(preferencesPath(options))
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	if value.Appearance != "system" && value.Appearance != "light" && value.Appearance != "dark" {
		value.Appearance = "system"
	}
	return value, nil
}
func savePreferences(options Options, value preferences) error {
	name := preferencesPath(options)
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(name), ".desktop-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := errors.Join(encoder.Encode(value), file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}
