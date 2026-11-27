package ownership

import (
	"errors"
	"time"
)

type Phase string

const (
	Local      Phase = "local"
	Preparing  Phase = "preparing"
	Active     Phase = "active"
	Recovering Phase = "recovering"
)

type NodeState string

const (
	NodeLocal      NodeState = "local"
	NodeRequesting NodeState = "requesting"
	NodePreparing  NodeState = "preparing-receive"
	NodeSending    NodeState = "sending"
	NodeReceiving  NodeState = "receiving"
	NodeRecovering NodeState = "recovering"
)

type Reason string

const (
	Returned       Reason = "returned"
	LocalTakeover  Reason = "local-takeover"
	Emergency      Reason = "emergency"
	Paused         Reason = "paused"
	Disconnected   Reason = "disconnected"
	TimedOut       Reason = "timed-out"
	Collision      Reason = "collision"
	Cancelled      Reason = "cancelled"
	PermissionLost Reason = "permission-lost"
	LayoutChanged  Reason = "layout-changed"
	Exhausted      Reason = "counter-exhausted"
)

var (
	ErrInvalid   = errors.New("控制权配置或参数无效")
	ErrUnknown   = errors.New("设备不属于当前认证会话")
	ErrStale     = errors.New("控制代次或会话已经失效")
	ErrBusy      = errors.New("控制权正在使用或等待恢复")
	ErrPaused    = errors.New("当前会话已暂停控制")
	ErrClosed    = errors.New("当前会话已关闭")
	ErrReplay    = errors.New("输入或续租序号已经处理")
	ErrClock     = errors.New("本机控制时钟无效或倒退")
	ErrNotSource = errors.New("当前设备不是输入来源")
	ErrExhausted = errors.New("控制代次已耗尽，必须重新建立会话")
)

type Config struct {
	Devices         [2]string
	SessionID       string
	PrepareTimeout  time.Duration
	LeaseTimeout    time.Duration
	InitiallyPaused []string
}

type Fence struct {
	Session string `json:"session"`
	Epoch   uint64 `json:"epoch"`
}

type Token struct {
	Fence
	Source    string `json:"source"`
	Target    string `json:"target"`
	RequestID uint64 `json:"request_id"`
}

type Request struct {
	Source string `json:"source"`
	ID     uint64 `json:"id"`
}

type Node struct {
	ID       string    `json:"id"`
	State    NodeState `json:"state"`
	Paused   bool      `json:"paused"`
	Ready    bool      `json:"ready"`
	Restored bool      `json:"restored"`
}

type Snapshot struct {
	Fence
	Coordinator     string    `json:"coordinator"`
	Phase           Phase     `json:"phase"`
	Connected       bool      `json:"connected"`
	Lease           *Token    `json:"lease,omitempty"`
	Previous        *Token    `json:"previous,omitempty"`
	Waiting         *Request  `json:"waiting,omitempty"`
	WaitingDeadline time.Time `json:"waiting_deadline,omitempty"`
	Deadline        time.Time `json:"deadline,omitempty"`
	Reason          Reason    `json:"reason,omitempty"`
	Nodes           [2]Node   `json:"nodes"`
}
