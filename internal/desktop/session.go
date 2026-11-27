package desktop

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"myapp/internal/config"
)

const Version = 1

type Peer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	OS           string `json:"os,omitempty"`
	Version      string `json:"version,omitempty"`
	Status       string `json:"status"`
	Control      bool   `json:"control"`
	FileResults  bool   `json:"file_results"`
	Edge         bool   `json:"edge"`
	ScreenWidth  int    `json:"screen_width,omitempty"`
	ScreenHeight int    `json:"screen_height,omitempty"`
}

type Task struct {
	ID        string    `json:"id"`
	PeerID    string    `json:"peer_id"`
	Name      string    `json:"name"`
	Direction string    `json:"direction"`
	State     string    `json:"state"`
	Bytes     int64     `json:"bytes"`
	Total     int64     `json:"total"`
	Path      string    `json:"path,omitempty"`
	Error     string    `json:"error,omitempty"`
	Updated   time.Time `json:"updated"`
}

func (t Task) Active() bool {
	return t.State != "completed" && t.State != "failed" && t.State != "cancelled" && t.State != "unconfirmed"
}

type Snapshot struct {
	Revision      uint64      `json:"revision"`
	Phase         string      `json:"phase"`
	DeviceID      string      `json:"device_id"`
	Role          config.Role `json:"role"`
	Target        string      `json:"target"`
	Paused        bool        `json:"paused"`
	Text          bool        `json:"text"`
	Files         bool        `json:"files"`
	Accepting     bool        `json:"accepting"`
	EdgeEnabled   bool        `json:"edge_enabled"`
	EdgeReady     bool        `json:"edge_ready"`
	EdgeStatus    string      `json:"edge_status,omitempty"`
	EdgeError     string      `json:"edge_error,omitempty"`
	Attempt       int         `json:"attempt"`
	Error         string      `json:"error,omitempty"`
	Peers         []Peer      `json:"peers"`
	Tasks         []Task      `json:"tasks"`
	PeerMode      bool        `json:"peer_mode,omitempty"`
	ControlPhase  string      `json:"control_phase,omitempty"`
	ControlSource string      `json:"control_source,omitempty"`
	ControlTarget string      `json:"control_target,omitempty"`
}

type Command struct {
	Version int               `json:"version"`
	ID      uint64            `json:"id"`
	Action  string            `json:"action"`
	Target  string            `json:"target,omitempty"`
	Enabled bool              `json:"enabled"`
	Layout  map[string]string `json:"layout,omitempty"`
}

type Message struct {
	Version   int      `json:"version"`
	RequestID uint64   `json:"request_id,omitempty"`
	Error     string   `json:"error,omitempty"`
	Snapshot  Snapshot `json:"snapshot"`
}

type Handler func(context.Context, Command) error

type Session struct {
	commandMu   sync.Mutex
	mu          sync.Mutex
	state       Snapshot
	handler     Handler
	bindID      uint64
	tasks       map[string]Task
	taskActions map[string]Handler
	senders     map[string]func([]string) error
	stopPending map[string]bool
}

func New(cfg config.Config, id string) *Session {
	s := &Session{state: Snapshot{Phase: "starting", DeviceID: id, Role: cfg.Role, Text: cfg.Clipboard.TextEnabled, Files: cfg.Files.Enabled, Accepting: true, EdgeEnabled: cfg.Role == config.RoleController}, tasks: make(map[string]Task), taskActions: make(map[string]Handler), senders: make(map[string]func([]string) error)}
	for _, p := range cfg.Peers {
		s.state.Peers = append(s.state.Peers, Peer{ID: p.ID, Address: p.Address, Name: p.ID, Status: "offline"})
	}
	return s
}

func (s *Session) Update(change func(*Snapshot)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.state)
	s.state.Revision++
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	state.Peers = append([]Peer(nil), state.Peers...)
	for _, task := range s.tasks {
		state.Tasks = append(state.Tasks, task)
	}
	sort.Slice(state.Tasks, func(i, j int) bool { return state.Tasks[i].Updated.After(state.Tasks[j].Updated) })
	return state
}

func (s *Session) ReportEdge(ready bool, status, failure string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.EdgeReady == ready && s.state.EdgeStatus == status && s.state.EdgeError == failure {
		return
	}
	s.state.EdgeReady, s.state.EdgeStatus, s.state.EdgeError = ready, status, failure
	s.state.Revision++
}

func (s *Session) Sharing() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{Paused: s.state.Paused, Text: s.state.Text, Files: s.state.Files, Accepting: s.state.Accepting, EdgeEnabled: s.state.EdgeEnabled, Target: s.state.Target, PeerMode: s.state.PeerMode, ControlPhase: s.state.ControlPhase, ControlSource: s.state.ControlSource, ControlTarget: s.state.ControlTarget}
}

func (s *Session) UpdatePeer(id string, update func(*Peer)) {
	s.Update(func(state *Snapshot) {
		for index := range state.Peers {
			if state.Peers[index].ID == id {
				update(&state.Peers[index])
				return
			}
		}
	})
}

func (s *Session) Peer(peer Peer) {
	s.Update(func(state *Snapshot) {
		for i, p := range state.Peers {
			if p.ID == peer.ID {
				state.Peers[i] = peer
				return
			}
		}
		state.Peers = append(state.Peers, peer)
	})
}

func (s *Session) Task(task Task, action Handler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task.Updated = time.Now().UTC()
	s.tasks[task.ID] = task
	if action != nil {
		s.taskActions[task.ID] = action
	}
	if len(s.tasks) > 100 {
		var oldest Task
		for _, t := range s.tasks {
			if !t.Active() && (oldest.ID == "" || t.Updated.Before(oldest.Updated)) {
				oldest = t
			}
		}
		if oldest.ID != "" {
			delete(s.tasks, oldest.ID)
			delete(s.taskActions, oldest.ID)
		}
	}
	s.state.Revision++
}

func (s *Session) Bind(handler Handler, initialize ...func()) func() {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	for _, init := range initialize {
		init()
	}
	s.mu.Lock()
	s.bindID++
	id := s.bindID
	s.handler = handler
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.bindID == id {
			s.handler = nil
		}
		s.mu.Unlock()
	}
}

func (s *Session) Apply(ctx context.Context, command Command) error {
	if command.Action == "prepare_stop" && command.Version == Version && command.ID != 0 {
		return s.prepareStop(ctx)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if command.Version != Version || command.ID == 0 {
		return errors.New("桌面命令版本或请求编号无效")
	}
	s.mu.Lock()
	handler := s.handler
	if command.Action == "cancel_task" || command.Action == "retry_task" {
		handler = s.taskActions[command.Target]
	}
	s.mu.Unlock()
	if handler == nil {
		switch command.Action {
		case "pause", "text", "files", "accept":
			s.Update(func(state *Snapshot) {
				switch command.Action {
				case "pause":
					state.Paused = command.Enabled
				case "text":
					state.Text = command.Enabled
				case "files":
					state.Files = command.Enabled
				case "accept":
					state.Accepting = command.Enabled
				}
			})
			return nil
		}
		return errors.New("服务尚未就绪，或任务已结束；请刷新状态后重试")
	}
	return handler(ctx, command)
}

func (s *Session) prepareStop(ctx context.Context) error {
	if err := s.Apply(ctx, Command{Version: Version, ID: 1, Action: "pause", Enabled: true}); err != nil {
		return err
	}
	for _, task := range s.Snapshot().Tasks {
		if task.Active() {
			s.mu.Lock()
			if s.stopPending == nil {
				s.stopPending = make(map[string]bool)
			}
			s.stopPending[task.ID] = true
			s.mu.Unlock()
			if err := s.Apply(ctx, Command{Version: Version, ID: 1, Action: "cancel_task", Target: task.ID}); err != nil {
				if taskStillActive(s.Snapshot(), task.ID) {
					return err
				}
			}
		}
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		active := false
		for _, task := range s.Snapshot().Tasks {
			active = active || task.Active()
			s.mu.Lock()
			pending := s.stopPending[task.ID]
			if task.State == "completed" || task.State == "cancelled" {
				delete(s.stopPending, task.ID)
				pending = false
			}
			s.mu.Unlock()
			if pending && task.State == "failed" {
				return errors.New("未能确认文件任务已取消：" + task.Error)
			}
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("未能确认所有任务已停止，请继续等待或明确选择强制退出")
		case <-ticker.C:
		}
	}
}

func taskStillActive(state Snapshot, id string) bool {
	for _, task := range state.Tasks {
		if task.ID == id {
			return task.Active()
		}
	}
	return false
}

func (s *Session) Disconnected(reason string) {
	s.Update(func(state *Snapshot) {
		state.Target = ""
		state.PeerMode = false
		state.ControlPhase, state.ControlSource, state.ControlTarget = "", "", ""
		state.Phase = "reconnecting"
		state.Error = reason
		for i := range state.Peers {
			state.Peers[i].Status = "offline"
			state.Peers[i].Control = false
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, task := range s.tasks {
		if task.Active() {
			task.State = "failed"
			task.Error = "连接中断，请重新发送"
			task.Updated = time.Now().UTC()
			s.tasks[id] = task
		}
	}
}

type contextKey struct{}

func (s *Session) SetSender(peer string, send func([]string) error) {
	s.mu.Lock()
	s.senders[peer] = send
	s.mu.Unlock()
}
func (s *Session) RetryFiles(peer string, paths []string) error {
	s.mu.Lock()
	send := s.senders[peer]
	s.mu.Unlock()
	if send == nil {
		return errors.New("目标电脑尚未连接")
	}
	return send(paths)
}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func From(ctx context.Context) *Session { s, _ := ctx.Value(contextKey{}).(*Session); return s }
