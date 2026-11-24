package instance

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestDuplicateProcessActivatesOwnerAndReleasesAfterExit(t *testing.T) {
	name := fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	if value := os.Getenv("MYAPP_INSTANCE_TEST"); value != "" {
		_, err := Acquire(value)
		if !errors.Is(err, ErrRunning) {
			t.Fatalf("重复进程未被阻止: %v", err)
		}
		return
	}
	owner, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	child := exec.Command(os.Args[0], "-test.run=^TestDuplicateProcessActivatesOwnerAndReleasesAfterExit$")
	child.Env = append(os.Environ(), "MYAPP_INSTANCE_TEST="+name)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("子进程失败: %v %s", err, output)
	}
	select {
	case <-owner.Activations:
	case <-time.After(3 * time.Second):
		t.Fatal("原实例没有收到唤起通知")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(name)
	if err != nil {
		t.Fatalf("退出后锁未释放: %v", err)
	}
	_ = next.Close()
}

func TestLockIsReleasedAfterOwnerProcessCrash(t *testing.T) {
	if name := os.Getenv("MYAPP_INSTANCE_CRASH_TEST"); name != "" {
		guard, err := Acquire(name)
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Close()
		fmt.Fprintln(os.Stdout, "READY")
		time.Sleep(time.Minute)
		return
	}
	name := fmt.Sprintf("crash-%d-%d", os.Getpid(), time.Now().UnixNano())
	child := exec.Command(os.Args[0], "-test.run=^TestLockIsReleasedAfterOwnerProcessCrash$")
	child.Env = append(os.Environ(), "MYAPP_INSTANCE_CRASH_TEST="+name)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	if !bufio.NewScanner(output).Scan() {
		t.Fatal("实例辅助进程未就绪")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	guard, err := Acquire(name)
	if err != nil {
		t.Fatalf("异常退出后不能重启: %v", err)
	}
	_ = guard.Close()
}
