package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
)

func runDesktopService(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("desktop-service", flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", defaultConfigPath(), "配置路径")
	identityDir := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	trustDir := set.String("trust-dir", defaultTrustDir(), "信任目录")
	if err := set.Parse(args); err != nil {
		return err
	}
	cfg, local, store, err := loadRuntime(*configPath, *identityDir, *trustDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if cfg.Files.Cache == "" {
		cfg.Files.Cache = filepath.Join(filepath.Dir(*configPath), "cache", "files")
	}
	state := desktop.New(cfg, local.DeviceID)
	ctx = desktop.WithSession(ctx, state)
	responses := make(chan desktop.Message, 16)
	var output sync.WaitGroup
	output.Add(1)
	go func() {
		defer output.Done()
		encoder := json.NewEncoder(stdout)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var revision uint64
		write := func(message desktop.Message) bool {
			if err := encoder.Encode(message); err != nil {
				cancel()
				return false
			}
			return true
		}
		for {
			select {
			case response := <-responses:
				if !write(response) {
					return
				}
			case <-ticker.C:
				snapshot := state.Snapshot()
				if snapshot.Revision != revision || revision == 0 {
					revision = snapshot.Revision
					if !write(desktop.Message{Version: desktop.Version, Snapshot: snapshot}) {
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer cancel()
		scanner := bufio.NewScanner(stdin)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var command desktop.Command
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&command); err != nil {
				return
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				return
			}
			var result error
			if command.Action == "stop" && command.Version == desktop.Version && command.ID != 0 {
				state.Update(func(s *desktop.Snapshot) { s.Phase = "stopping"; s.Target = "" })
				cancel()
				return
			}
			requestCtx, done := context.WithTimeout(ctx, 10*time.Second)
			result = state.Apply(requestCtx, command)
			done()
			message := desktop.Message{Version: desktop.Version, RequestID: command.ID, Snapshot: state.Snapshot()}
			if result != nil {
				message.Error = result.Error()
			}
			select {
			case responses <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	if cfg.Role == config.RoleController {
		err = runController(ctx, cfg, local, store, stderr)
	} else {
		err = runAgent(ctx, cfg, local, store, stderr, stderr)
	}
	cancel()
	output.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
