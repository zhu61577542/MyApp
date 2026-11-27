package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"myapp/internal/app"
	"myapp/internal/identity"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/pointer"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

// 此探针只验证协议，所有输入进入计数器，不调用系统注入器。
type probeDriver struct{ inputs atomic.Int64 }

func (*probeDriver) Prepare(context.Context, ownership.Token, bool, *pointer.Entry, func(common.Event) error, func(bool)) error {
	return nil
}
func (*probeDriver) Activate(context.Context, ownership.Token) error { return nil }
func (*probeDriver) Restore(context.Context) error                   { return nil }
func (d *probeDriver) Inject(common.Event) error                     { d.inputs.Add(1); return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "", "init/serve/connect")
	root := flag.String("root", "", "隔离测试目录")
	address := flag.String("addr", "", "测试连接地址")
	peerCert := flag.String("peer-cert", "", "对端测试证书")
	flag.Parse()
	if *mode != "init" && *mode != "serve" && *mode != "connect" {
		return errors.New("探针模式无效")
	}
	if *root == "" {
		return errors.New("必须指定隔离测试目录")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(strings.ToLower(abs), `e:\test\`) {
		return errors.New("Windows探针仅允许E:\\test内目录")
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	idPath := filepath.Join(abs, "identity")
	if *mode == "init" {
		local, err := identity.Generate("MyApp isolated protocol probe", time.Now())
		if err != nil {
			return err
		}
		if err := identity.SaveNew(idPath, local); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(abs, "public.der"), local.CertDER, 0600); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			DeviceID    string `json:"device_id"`
			Certificate []byte `json:"certificate"`
		}{local.DeviceID, local.CertDER})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	local, err := identity.Load(idPath)
	if err != nil {
		return err
	}
	der, err := os.ReadFile(*peerCert)
	if err != nil {
		return err
	}
	store := trust.New(filepath.Join(abs, "trust"))
	record, err := store.VerifyCertificate(der, time.Now())
	if errors.Is(err, trust.ErrNotTrusted) {
		record, err = store.AddNew(der, time.Now())
	}
	if err != nil {
		return err
	}
	var connections [3]*tls.Conn
	if *mode == "serve" {
		listener, err := net.Listen("tcp", *address)
		if err != nil {
			return err
		}
		defer listener.Close()
		context.AfterFunc(ctx, func() { listener.Close() })
		fmt.Fprintln(os.Stderr, "READY protocol listener", listener.Addr())
		for i := range connections {
			raw, err := listener.Accept()
			if err != nil {
				return err
			}
			connections[i] = tls.Server(raw, transport.ServerTLS(local, store))
			if err := connections[i].HandshakeContext(ctx); err != nil {
				raw.Close()
				return err
			}
		}
	} else {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: transport.ClientTLS(local, record.DeviceID, store)}
		for i := range connections {
			raw, err := dialer.DialContext(ctx, "tcp", *address)
			if err != nil {
				return err
			}
			connections[i] = raw.(*tls.Conn)
		}
	}
	defer func() {
		for _, conn := range connections {
			if conn != nil {
				conn.Close()
			}
		}
	}()
	for _, conn := range connections {
		peer, err := transport.PeerDeviceID(conn.ConnectionState())
		if err != nil || peer != record.DeviceID {
			return errors.New("测试通道认证身份不一致")
		}
	}
	driver := &probeDriver{}
	device := peercontrol.NewDevice(driver)
	link, err := app.NewSplitLink(nil, nil)
	if err != nil {
		return err
	}
	if err := link.EnablePeerControl(app.PeerOptions{LocalID: local.DeviceID, RemoteID: record.DeviceID, Device: device}); err != nil {
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- link.RunChannels(ctx, connections[0], connections[1]) }()
	defer func() {
		cancel()
		select {
		case <-ended:
		case <-time.After(time.Second):
		}
	}()
	node, err := link.PeerControl(ctx)
	if err != nil {
		return err
	}
	if node == nil {
		return errors.New("未协商对等控制")
	}
	deadline, _ := ctx.Deadline()
	if err := connections[2].SetDeadline(deadline); err != nil {
		return err
	}
	barrier := bufio.NewReadWriter(bufio.NewReader(connections[2]), bufio.NewWriter(connections[2]))
	send := func(text string) error {
		if _, err := fmt.Fprintln(barrier, text); err != nil {
			return err
		}
		return barrier.Flush()
	}
	receive := func(expected string) error {
		text, err := barrier.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != expected {
			return fmt.Errorf("核查屏障不匹配: %q", text)
		}
		return nil
	}
	wait := func(predicate func() bool) error {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for !predicate() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		return nil
	}
	start := func(kind common.Kind) error {
		if err := link.RequestControl(ctx, "begin"); err != nil {
			return err
		}
		code := uint16(4)
		up := common.KeyUp
		if kind == common.MouseButtonDown {
			code = 1
			up = common.MouseButtonUp
		}
		for _, event := range []common.Event{{Kind: kind, Code: code, Sequence: 1}, {Kind: up, Code: code, Sequence: 2}} {
			if err := link.SendInput(ctx, event); err != nil {
				return err
			}
		}
		return nil
	}
	stop := func() error {
		if err := link.RequestControl(ctx, "end"); err != nil {
			return err
		}
		return wait(func() bool { return node.Snapshot().Phase == ownership.Local })
	}
	if *mode == "serve" {
		if err := start(common.KeyDown); err != nil {
			return err
		}
		if err := send("mac-sent"); err != nil {
			return err
		}
		if err := receive("mac-received"); err != nil {
			return err
		}
		if err := stop(); err != nil {
			return err
		}
		if err := send("mac-restored"); err != nil {
			return err
		}
		if err := receive("windows-sent"); err != nil {
			return err
		}
		if err := wait(func() bool { return driver.inputs.Load() == 2 }); err != nil {
			return err
		}
		if err := send("windows-received"); err != nil {
			return err
		}
		if err := receive("windows-restored"); err != nil {
			return err
		}
		if err := wait(func() bool { return node.Snapshot().Phase == ownership.Local }); err != nil {
			return err
		}
		if err := send("done"); err != nil {
			return err
		}
		if err := receive("done"); err != nil {
			return err
		}
	} else {
		if err := receive("mac-sent"); err != nil {
			return err
		}
		if err := wait(func() bool { return driver.inputs.Load() == 2 }); err != nil {
			return err
		}
		if err := send("mac-received"); err != nil {
			return err
		}
		if err := receive("mac-restored"); err != nil {
			return err
		}
		if err := wait(func() bool { return node.Snapshot().Phase == ownership.Local }); err != nil {
			return err
		}
		if err := start(common.MouseButtonDown); err != nil {
			return err
		}
		if err := send("windows-sent"); err != nil {
			return err
		}
		if err := receive("windows-received"); err != nil {
			return err
		}
		if err := stop(); err != nil {
			return err
		}
		if err := send("windows-restored"); err != nil {
			return err
		}
		if err := receive("done"); err != nil {
			return err
		}
		if err := send("done"); err != nil {
			return err
		}
	}
	if err := device.WaitRestored(ctx); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Success        bool   `json:"success"`
		OS             string `json:"os"`
		Sent, Received int
		Mode           string `json:"mode"`
		PhysicalInput  bool   `json:"physical_input"`
	}{true, runtime.GOOS, 2, int(driver.inputs.Load()), *mode, false})
}
