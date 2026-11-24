package main

import (
	"encoding/json"
	"flag"
	"io"

	"myapp/internal/config"
	"myapp/internal/platform"
	"myapp/internal/pointer"
)

func runDesktopDiagnostics(args []string, output, stderr io.Writer) error {
	set := flag.NewFlagSet("desktop-diagnostics", flag.ContinueOnError)
	set.SetOutput(stderr)
	path := set.String("config", defaultConfigPath(), "配置路径")
	if err := set.Parse(args); err != nil {
		return err
	}
	result := map[string]any{"version": version}
	if cfg, err := config.Load(*path); err == nil {
		positions := make([]string, 0, len(cfg.Peers))
		for _, peer := range cfg.Peers {
			positions = append(positions, peer.Position)
		}
		result["role"], result["positions"] = cfg.Role, positions
	}
	capture, err := platform.NewCapturer()
	result["capture_ready"] = err == nil
	if err != nil {
		result["capture_error"] = err.Error()
	} else {
		_ = capture.Close()
	}
	injector, err := platform.NewInjector()
	result["inject_ready"] = err == nil
	if err != nil {
		result["inject_error"] = err.Error()
	} else {
		_ = injector.Close()
	}
	geometry, err := pointer.New()
	if err == nil {
		if topology, e := pointer.TopologyFor(geometry); e == nil {
			result["topology"] = topology
		}
		_, err = geometry.Position()
	}
	result["pointer_readable"] = err == nil
	if err != nil {
		result["pointer_error"] = err.Error()
	}
	return json.NewEncoder(output).Encode(result)
}
