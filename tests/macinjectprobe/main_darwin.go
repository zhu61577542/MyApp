//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"

	darwininput "myapp/internal/platform/darwin/input"
)

func main() {
	injector, err := darwininput.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer injector.Close()
	capturer, err := darwininput.NewCapturer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := struct {
		FrameworkLoaded bool `json:"framework_loaded"`
		CanInject       bool `json:"can_inject"`
		CanCapture      bool `json:"can_capture"`
		PassiveOnly     bool `json:"passive_only"`
	}{FrameworkLoaded: true, CanInject: injector.CanInject(), CanCapture: capturer.CanCapture(), PassiveOnly: true}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
