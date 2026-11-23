package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"myapp/internal/pointer"
	"myapp/internal/protocol"
	"myapp/internal/transport"
)

type Capabilities struct {
	Version         int    `json:"version"`
	Name            string `json:"name"`
	OS              string `json:"os"`
	SoftwareVersion string `json:"software_version,omitempty"`
	Control         bool   `json:"control"`
	FileResults     bool   `json:"file_results"`
}

type controlPacket struct {
	ID     uint64         `json:"id"`
	Action string         `json:"action,omitempty"`
	Error  string         `json:"error,omitempty"`
	Entry  *pointer.Entry `json:"entry,omitempty"`
}

type linkDesktop struct {
	mu             sync.Mutex
	local          Capabilities
	remote         Capabilities
	onCapabilities func(Capabilities)
	control        func(context.Context, string) error
	ready          chan struct{}
	readyOnce      sync.Once
	next           atomic.Uint64
	pending        map[uint64]chan error
	onControlEnded func()
	edgeControl    func(context.Context, string, *pointer.Entry) error
}

func (l *Link) SetEdgeControlHandler(handler func(context.Context, string, *pointer.Entry) error) {
	if l.desktop != nil {
		l.desktop.edgeControl = handler
	}
}

// 仅在启动 Run 前设置。
func (l *Link) SetControlEndedHandler(handler func()) {
	if l.desktop != nil {
		l.desktop.onControlEnded = handler
	}
}

func (l *Link) NotifyControlEnded(ctx context.Context) error {
	payload, _ := json.Marshal(controlPacket{Action: "ended"})
	return l.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypeControlResult, Payload: payload})
}

func (l *Link) EnableDesktop(local Capabilities, onCapabilities func(Capabilities), control func(context.Context, string) error) {
	local.Version = 1
	l.desktop = &linkDesktop{local: local, onCapabilities: onCapabilities, control: control, ready: make(chan struct{}), pending: make(map[uint64]chan error)}
}

func (l *Link) RemoteCapabilities() Capabilities {
	if l.desktop == nil {
		return Capabilities{}
	}
	l.desktop.mu.Lock()
	defer l.desktop.mu.Unlock()
	return l.desktop.remote
}

func (l *Link) sendCapabilities(ctx context.Context) {
	payload, _ := json.Marshal(l.desktop.local)
	_ = l.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypeCapabilities, Payload: payload})
}

func (l *Link) RequestControl(ctx context.Context, action string) error {
	return l.requestControl(ctx, action, nil)
}

func (l *Link) RequestEdgeControl(ctx context.Context, entry pointer.Entry) error {
	remote := l.RemoteScreen()
	if remote.Version != 1 || !remote.Screen.Valid() || !entry.Valid() || entry.ScreenKey != remote.Screen.Key() {
		return errors.New("对端尚未提供有效跨屏能力，请更新两端软件或等待屏幕信息")
	}
	return l.requestControl(ctx, "begin", &entry)
}

func (l *Link) requestControl(ctx context.Context, action string, entry *pointer.Entry) error {
	state := l.desktop
	if state == nil {
		return errors.New("对端未声明桌面控制能力，请两端均使用新版桌面界面后重新连接")
	}
	select {
	case <-state.ready:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return errors.New("对端未声明桌面控制能力，请两端均使用新版桌面界面后重新连接")
	}
	if !l.RemoteCapabilities().Control {
		return errors.New("对端当前模式不接受控制，请确认另一台电脑使用新版桌面界面的子电脑模式")
	}
	id := state.next.Add(1)
	response := make(chan error, 1)
	state.mu.Lock()
	state.pending[id] = response
	state.mu.Unlock()
	defer func() { state.mu.Lock(); delete(state.pending, id); state.mu.Unlock() }()
	payload, _ := json.Marshal(controlPacket{ID: id, Action: action, Entry: entry})
	if err := l.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypeControlRequest, Payload: payload}); err != nil {
		return err
	}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Link) handleDesktopFrame(ctx context.Context, frame protocol.Frame) error {
	if l.desktop == nil {
		return nil
	}
	state := l.desktop
	if len(frame.Payload) > 4096 {
		return errors.New("桌面会话消息过长")
	}
	if frame.Type == protocol.TypeCapabilities {
		var capabilities Capabilities
		if err := decodeStrictJSON(frame.Payload, &capabilities); err != nil {
			return err
		}
		if capabilities.Version != 1 || len(capabilities.Name) > 256 || len(capabilities.OS) > 32 || len(capabilities.SoftwareVersion) > 128 {
			return errors.New("桌面能力信息无效")
		}
		state.mu.Lock()
		state.remote = capabilities
		state.mu.Unlock()
		state.readyOnce.Do(func() { close(state.ready) })
		if state.onCapabilities != nil {
			state.onCapabilities(capabilities)
		}
		return nil
	}
	var packet controlPacket
	if err := decodeStrictJSON(frame.Payload, &packet); err != nil {
		return err
	}
	if frame.Type == protocol.TypeControlResult && packet.ID == 0 && packet.Action == "ended" {
		if state.onControlEnded != nil {
			state.onControlEnded()
		}
		return nil
	}
	if packet.ID == 0 {
		return errors.New("控制请求编号无效")
	}
	if frame.Type == protocol.TypeControlResult {
		state.mu.Lock()
		response := state.pending[packet.ID]
		state.mu.Unlock()
		if response != nil {
			var err error
			if packet.Error != "" {
				err = errors.New(packet.Error)
			}
			select {
			case response <- err:
			default:
			}
		}
		return nil
	}
	if packet.Action != "begin" && packet.Action != "end" {
		return errors.New("控制请求动作无效")
	}
	var err error
	if packet.Entry != nil {
		if packet.Action != "begin" || !packet.Entry.Valid() || state.edgeControl == nil {
			err = errors.New("当前会话不支持该跨屏入口")
		} else {
			err = state.edgeControl(ctx, packet.Action, packet.Entry)
		}
	} else if state.control == nil {
		err = errors.New("这台电脑不接受键鼠控制")
	} else {
		err = state.control(ctx, packet.Action)
	}
	result := controlPacket{ID: packet.ID}
	if err != nil {
		result.Error = err.Error()
	}
	payload, _ := json.Marshal(result)
	return l.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypeControlResult, Payload: payload})
}
