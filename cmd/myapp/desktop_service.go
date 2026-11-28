package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/instance"
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
	guard, err := instance.Acquire("desktop-service")
	if err != nil {
		return fmt.Errorf("共享服务已运行或无法取得实例锁: %w", err)
	}
	defer guard.Close()
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
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		ctx = withDesktopPeer(ctx, state)
	}
	responses := make(chan desktop.Message, 16)
	var output sync.WaitGroup
	output.Add(1)
	go func() {
		defer output.Done()
		encoder := json.NewEncoder(stdout)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var revision uint64
		var edgeDiagnostic string
		var controlDiagnostic string
		var nextInputDiagnostic time.Time
		flushDiagnostics := func() {
			for _, diagnostic := range state.DrainDiagnostics() {
				data, err := json.Marshal(diagnostic)
				if err == nil {
					fmt.Fprintln(stderr, "跨屏诊断："+string(data))
				}
			}
		}
		defer flushDiagnostics()
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
				if !time.Now().Before(nextInputDiagnostic) {
					if peer := desktopPeerFrom(ctx); peer != nil {
						peer.driver.reportInputCounts()
					}
					nextInputDiagnostic = time.Now().Add(2 * time.Second)
				}
				flushDiagnostics()
				snapshot := state.Snapshot()
				diagnostic := snapshot.EdgeStatus + ": " + snapshot.EdgeError
				if snapshot.EdgeStatus != "" && diagnostic != edgeDiagnostic {
					fmt.Fprintln(stderr, "跨屏状态："+diagnostic)
					edgeDiagnostic = diagnostic
				}
				control := snapshot.ControlPhase + ": " + snapshot.ControlSource + " -> " + snapshot.ControlTarget
				if snapshot.PeerMode && control != controlDiagnostic {
					fmt.Fprintln(stderr, "对等控制状态："+control)
					controlDiagnostic = control
				}
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
			if result == nil && command.Action == "pause" {
				if peer := desktopPeerFrom(ctx); peer != nil {
					peer.device.SetPaused(command.Enabled)
				}
			}
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
