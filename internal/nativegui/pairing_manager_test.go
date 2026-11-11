//go:build gui

package nativegui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/identity"
)

func TestPairingProcessHelper(t *testing.T) {
	if os.Getenv("MYAPP_PAIRING_HELPER") != "1" {
		return
	}
	fmt.Println("配对码: 123456")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		os.Exit(1)
	}
	fmt.Println(os.Getenv("MYAPP_PAIRING_RESULT"))
	os.Exit(0)
}

func startPairingTestProcess(t *testing.T, options Options) (*pairingManager, <-chan struct{}, string) {
	t.Helper()
	remote, err := identity.Generate("remote", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestPairingProcessHelper$")
	command.Env = append(os.Environ(), "MYAPP_PAIRING_HELPER=1",
		fmt.Sprintf("MYAPP_PAIRING_RESULT=配对完成: remote，设备 ID %s，地址 127.0.0.1:24800", remote.DeviceID))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	manager := &pairingManager{cmd: command, input: input}
	done := make(chan struct{})
	go func() {
		manager.collect(options, command, stdout, stderr)
		close(done)
	}()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		waitForPairing(t, done)
	})
	return manager, done, remote.DeviceID
}

func waitForPairing(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("配对进程未退出")
	}
}

func pairingTestOptions(t *testing.T, role config.Role) Options {
	t.Helper()
	options := Options{ConfigPath: filepath.Join(t.TempDir(), "config.json")}
	if err := config.SaveNew(options.ConfigPath, config.Default("local", role)); err != nil {
		t.Fatal(err)
	}
	return options
}

func TestPairingProcessExitDuringSnapshot(t *testing.T) {
	options := pairingTestOptions(t, config.RoleAgent)
	for iteration := 0; iteration < 10; iteration++ {
		manager, done, _ := startPairingTestProcess(t, options)
		if err := manager.confirm(); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
	loop:
		for {
			select {
			case <-done:
				break loop
			case <-deadline:
				t.Fatal("配对进程未退出")
			default:
				manager.snapshot()
				runtime.Gosched()
			}
		}
		if snapshot := manager.snapshot(); snapshot.Running || snapshot.Status != "配对完成" {
			t.Fatalf("退出状态错误: %+v", snapshot)
		}
	}
}

func TestBackgroundPairingPersistsPeerAfterWindowClosed(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	options := pairingTestOptions(t, config.RoleController)
	manager, done, remoteID := startPairingTestProcess(t, options)
	openPairing(application, options, manager)
	windows := application.Driver().AllWindows()
	windows[len(windows)-1].Close()
	if err := manager.confirm(); err != nil {
		t.Fatal(err)
	}
	waitForPairing(t, done)
	current, err := config.Load(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Peers) != 1 || current.Peers[0].ID != remoteID || current.Peers[0].Address != "127.0.0.1:24800" {
		t.Fatalf("后台配对未保存正确设备: %+v", current.Peers)
	}
	if snapshot := manager.snapshot(); !strings.Contains(snapshot.Status, "已将设备加入") {
		t.Fatalf("未保留保存结果: %+v", snapshot)
	}
}

func TestPairingSaveFailureRemainsVisible(t *testing.T) {
	options := Options{ConfigPath: filepath.Join(t.TempDir(), "missing.json")}
	manager, done, _ := startPairingTestProcess(t, options)
	if err := manager.confirm(); err != nil {
		t.Fatal(err)
	}
	waitForPairing(t, done)
	if snapshot := manager.snapshot(); snapshot.Running || !strings.Contains(snapshot.Status, "保存设备信息失败") {
		t.Fatalf("未保留保存失败状态: %+v", snapshot)
	}
}

func TestSavingSettingsKeepsBackgroundPairedPeer(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	options := pairingTestOptions(t, config.RoleController)
	openSettings(application, options, func() {})
	windows := application.Driver().AllWindows()
	window := windows[len(windows)-1]
	defer window.Close()
	manager, done, remoteID := startPairingTestProcess(t, options)
	if err := manager.confirm(); err != nil {
		t.Fatal(err)
	}
	waitForPairing(t, done)
	var save *widget.Button
	var name *widget.Entry
	var visit func(fyne.CanvasObject)
	visit = func(object fyne.CanvasObject) {
		switch object := object.(type) {
		case *fyne.Container:
			for _, child := range object.Objects {
				visit(child)
			}
		case *widget.Entry:
			if object.Text == "local" {
				name = object
			}
		case *widget.Button:
			if object.Text == "保存设置" {
				save = object
			}
		}
	}
	visit(window.Content())
	if save == nil || name == nil {
		t.Fatal("未找到设备名称或保存按钮")
	}
	name.SetText("renamed")
	test.Tap(save)
	current, err := config.Load(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if current.DeviceName != "renamed" || len(current.Peers) != 1 || current.Peers[0].ID != remoteID {
		t.Fatalf("保存设置覆盖了后台配对设备: %+v", current)
	}
}

func TestStoppedPairingDoesNotSavePeer(t *testing.T) {
	options := pairingTestOptions(t, config.RoleController)
	manager, done, _ := startPairingTestProcess(t, options)
	manager.stop()
	waitForPairing(t, done)
	current, err := config.Load(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Peers) != 0 || manager.snapshot().Status != "配对进程已终止" {
		t.Fatal("终止配对后状态错误或保存了设备")
	}
}
