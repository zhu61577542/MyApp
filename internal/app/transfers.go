package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"myapp/internal/protocol"
	"myapp/internal/transfer"
	"myapp/internal/transport"
)

var ErrTransferTask = errors.New("文件任务未完成")

type TransferEvent struct {
	ID        string
	StreamID  uint64
	Direction string
	Name      string
	State     string
	Bytes     int64
	Total     int64
	Path      string
	Error     string
	Sources   []string
}

type TransferObserver func(TransferEvent, func(context.Context) error)

type outgoingTransfer struct {
	ctx           context.Context
	parent        context.Context
	cancel        context.CancelFunc
	event         TransferEvent
	sources       []transfer.Source
	result        chan fileResult
	peerCancelled atomic.Bool
}

type receivedTransfer struct {
	manifest     transfer.Manifest
	receiver     *transfer.Receiver
	event        TransferEvent
	rejected     bool
	cancelling   bool
	lastProgress time.Time
}

type linkTransfers struct {
	mu         sync.Mutex
	outgoing   map[uint64]*outgoingTransfer
	incoming   map[uint64]*receivedTransfer
	finished   map[uint64]fileResult
	observe    TransferObserver
	allow      func() bool
	resultMu   sync.Mutex
	results    map[resultKey]fileResult
	resultWake chan struct{}
}

type resultKey struct {
	stream     uint64
	transferID string
}

type fileResult struct {
	TransferID string `json:"transfer_id"`
	State      string `json:"state"`
	Bytes      int64  `json:"bytes"`
	Total      int64  `json:"total"`
	Error      string `json:"error,omitempty"`
}
type fileCancel struct {
	TransferID string `json:"transfer_id"`
	Origin     string `json:"origin"`
}

func (l *Link) ObserveTransfers(observer TransferObserver, allow func() bool) {
	l.tasks = &linkTransfers{outgoing: make(map[uint64]*outgoingTransfer), incoming: make(map[uint64]*receivedTransfer), finished: make(map[uint64]fileResult), observe: observer, allow: allow, results: make(map[resultKey]fileResult), resultWake: make(chan struct{}, 1)}
}

func (l *Link) reportTransfer(event TransferEvent) {
	if l.tasks == nil || l.tasks.observe == nil {
		return
	}
	l.tasks.observe(event, func(ctx context.Context) error { return l.CancelTransfer(ctx, event.StreamID, event.Direction) })
}

func (l *Link) newOutgoing(ctx context.Context, id string, sources []transfer.Source) (*outgoingTransfer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, errors.New("没有可发送的源文件")
	}
	taskCtx, cancel := context.WithCancel(ctx)
	state := &outgoingTransfer{ctx: taskCtx, parent: ctx, cancel: cancel, sources: append([]transfer.Source(nil), sources...), result: make(chan fileResult, 8), event: TransferEvent{ID: id, StreamID: l.nextStreamID(), Direction: "send", Name: filepath.Base(sources[0].Path), State: "preparing"}}
	for _, source := range sources {
		state.event.Sources = append(state.event.Sources, source.Path)
	}
	l.tasks.mu.Lock()
	if len(l.tasks.outgoing) >= 8 {
		l.tasks.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("%w: 同时发送的任务过多，请稍后重新发送", ErrTransferTask)
	}
	l.tasks.outgoing[state.event.StreamID] = state
	l.tasks.mu.Unlock()
	l.reportTransfer(state.event)
	return state, nil
}

func (l *Link) QueueFiles(ctx context.Context, id string, sources []transfer.Source) error {
	if l.tasks == nil {
		return errors.New("文件任务监控未启用")
	}
	state, err := l.newOutgoing(ctx, id, sources)
	if err != nil {
		return err
	}
	go func() { _ = l.runOutgoing(state) }()
	return nil
}

func (l *Link) sendTrackedFiles(ctx context.Context, id string, sources []transfer.Source) error {
	state, err := l.newOutgoing(ctx, id, sources)
	if err != nil {
		return err
	}
	return l.runOutgoing(state)
}

func (l *Link) runOutgoing(task *outgoingTransfer) (resultErr error) {
	stream := task.event.StreamID
	defer func() { task.cancel(); l.tasks.mu.Lock(); delete(l.tasks.outgoing, stream); l.tasks.mu.Unlock() }()
	manifest, err := transfer.BuildManifestContext(task.ctx, task.event.ID, task.sources, time.Now())
	if l.desktop != nil {
		select {
		case <-l.desktop.ready:
		case <-time.After(time.Second):
		case <-task.ctx.Done():
		}
	}
	if err == nil {
		var total int64
		for _, entry := range manifest.Entries {
			if entry.Size > math.MaxInt64-total {
				err = errors.New("任务总大小超过可表示范围")
				break
			}
			total += entry.Size
		}
		l.tasks.mu.Lock()
		task.event.Total = total
		task.event.State = "transferring"
		event := task.event
		l.tasks.mu.Unlock()
		l.reportTransfer(event)
		if err == nil {
			err = l.sendManifest(task.ctx, manifest, task.sources, stream)
		}
	}
	if err != nil {
		select {
		case result := <-task.result:
			return l.finishOutgoing(task, result)
		default:
		}
		if task.parent.Err() != nil {
			return l.finishOutgoing(task, fileResult{State: "failed", Error: "连接已结束，请重新发送"})
		}
		if task.peerCancelled.Load() {
			_ = l.sendFileResult(task.parent, stream, fileResult{TransferID: task.event.ID, State: "cancelled"})
			return l.finishOutgoing(task, fileResult{State: "cancelled"})
		}
		if errors.Is(err, context.Canceled) {
			if sendErr := l.sendFileCancel(task.parent, stream, fileCancel{TransferID: task.event.ID, Origin: "sender"}); sendErr != nil {
				return l.finishOutgoing(task, fileResult{State: "failed", Error: sendErr.Error()})
			}
			return l.waitTransferResult(task, 5*time.Second)
		}
		_ = l.sendFileCancel(task.parent, stream, fileCancel{TransferID: task.event.ID, Origin: "sender"})
		return l.finishOutgoing(task, fileResult{State: "failed", Error: err.Error()})
	}
	if !l.RemoteCapabilities().FileResults {
		l.tasks.mu.Lock()
		task.event.State = "unconfirmed"
		event := task.event
		l.tasks.mu.Unlock()
		l.reportTransfer(event)
		return nil
	}
	return l.waitTransferResult(task, 0)
}

func (l *Link) waitTransferResult(task *outgoingTransfer, timeout time.Duration) error {
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		select {
		case result := <-task.result:
			return l.finishOutgoing(task, result)
		case <-task.parent.Done():
			return l.finishOutgoing(task, fileResult{State: "failed", Error: "连接中断，请重新发送"})
		case <-deadline:
			return l.finishOutgoing(task, fileResult{State: "failed", Error: "未收到对端取消确认，请检查连接"})
		case <-task.ctx.Done():
			if task.peerCancelled.Load() {
				_ = l.sendFileResult(task.parent, task.event.StreamID, fileResult{TransferID: task.event.ID, State: "cancelled"})
				return l.finishOutgoing(task, fileResult{State: "cancelled"})
			}
			select {
			case result := <-task.result:
				return l.finishOutgoing(task, result)
			default:
			}
			if err := l.sendFileCancel(task.parent, task.event.StreamID, fileCancel{TransferID: task.event.ID, Origin: "sender"}); err != nil {
				return l.finishOutgoing(task, fileResult{State: "failed", Error: err.Error()})
			}
			select {
			case result := <-task.result:
				return l.finishOutgoing(task, result)
			case <-task.parent.Done():
				return l.finishOutgoing(task, fileResult{State: "failed", Error: "连接中断，无法确认取消"})
			case <-time.After(5 * time.Second):
				return l.finishOutgoing(task, fileResult{State: "failed", Error: "未收到取消确认"})
			}
		}
	}
}

func (l *Link) finishOutgoing(task *outgoingTransfer, result fileResult) error {
	if result.State == "failed" && task.parent.Err() == nil {
		_ = l.sendFileCancel(task.parent, task.event.StreamID, fileCancel{TransferID: task.event.ID, Origin: "sender"})
	}
	l.tasks.mu.Lock()
	task.event.State = result.State
	task.event.Error = result.Error
	if result.State == "completed" {
		task.event.Bytes = task.event.Total
	}
	event := task.event
	l.tasks.mu.Unlock()
	l.reportTransfer(event)
	if result.State != "completed" {
		return fmt.Errorf("%w: %s", ErrTransferTask, result.Error)
	}
	return nil
}

func (l *Link) sendFileResult(ctx context.Context, stream uint64, result fileResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(result.Error) > 512 {
		result.Error = string([]rune(result.Error)[:min(128, len([]rune(result.Error)))])
	}
	l.tasks.resultMu.Lock()
	key := resultKey{stream: stream, transferID: result.TransferID}
	if len(l.tasks.results) >= 256 {
		if _, exists := l.tasks.results[key]; !exists {
			l.tasks.resultMu.Unlock()
			return errors.New("文件结果队列已满")
		}
	}
	l.tasks.results[key] = result
	l.tasks.resultMu.Unlock()
	select {
	case l.tasks.resultWake <- struct{}{}:
	default:
	}
	return nil
}

func (l *Link) runFileResults(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.tasks.resultWake:
		}
		for {
			l.tasks.resultMu.Lock()
			var stream uint64
			var result fileResult
			found := false
			for id, pending := range l.tasks.results {
				stream, result, found = id.stream, pending, true
				delete(l.tasks.results, id)
				break
			}
			l.tasks.resultMu.Unlock()
			if !found {
				break
			}
			if err := l.writeFileResult(ctx, stream, result); err != nil {
				return err
			}
		}
	}
}

func (l *Link) writeFileResult(ctx context.Context, stream uint64, result fileResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	writer := l.bulkWriter
	if writer == nil {
		writer = l.writer
	}
	return writer.Enqueue(ctx, transport.PriorityBulk, protocol.Frame{Type: protocol.TypeFileResult, StreamID: stream, Payload: payload})
}
func (l *Link) sendFileCancel(ctx context.Context, stream uint64, cancel fileCancel) error {
	payload, err := json.Marshal(cancel)
	if err != nil {
		return err
	}
	return l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileCancel, stream, payload)
}

func (l *Link) CancelTransfer(ctx context.Context, stream uint64, direction string) error {
	if !l.RemoteCapabilities().FileResults {
		return errors.New("对端不支持取消结果确认，请更新两端软件")
	}
	if direction == "send" {
		l.tasks.mu.Lock()
		state := l.tasks.outgoing[stream]
		if state == nil {
			l.tasks.mu.Unlock()
			return errors.New("任务已结束")
		}
		state.event.State = "cancelling"
		event := state.event
		state.cancel()
		l.tasks.mu.Unlock()
		l.reportTransfer(event)
		return nil
	}
	l.transferMu.Lock()
	state := l.tasks.incoming[stream]
	if state == nil {
		l.transferMu.Unlock()
		return errors.New("任务已结束")
	}
	if state.receiver != nil {
		if err := state.receiver.Cancel(); err != nil {
			l.transferMu.Unlock()
			return err
		}
	}
	state.rejected = true
	state.cancelling = true
	state.event.State = "cancelling"
	l.reportTransfer(state.event)
	id := state.manifest.TransferID
	l.transferMu.Unlock()
	return l.sendFileCancel(ctx, stream, fileCancel{TransferID: id, Origin: "receiver"})
}

func (l *Link) handleTransferResult(_ context.Context, frame protocol.Frame) error {
	if l.tasks == nil {
		return nil
	}
	var result fileResult
	if len(frame.Payload) > 4096 {
		return errors.New("文件结果消息过长")
	}
	if err := decodeStrictJSON(frame.Payload, &result); err != nil {
		return err
	}
	if result.TransferID == "" || result.Bytes < 0 || result.Total < 0 || result.Bytes > result.Total {
		return errors.New("文件结果无效")
	}
	terminal := result.State == "completed" || result.State == "failed" || result.State == "cancelled"
	if !terminal && result.State != "transferring" && result.State != "verifying" && result.State != "publishing" {
		return errors.New("文件结果状态无效")
	}
	l.tasks.mu.Lock()
	state := l.tasks.outgoing[frame.StreamID]
	if state != nil && state.event.ID == result.TransferID {
		if terminal {
			select {
			case state.result <- result:
			default:
			}
			if result.State != "completed" {
				state.cancel()
			}
		} else {
			state.event.State = result.State
			state.event.Bytes = result.Bytes
			event := state.event
			l.tasks.mu.Unlock()
			l.reportTransfer(event)
			return nil
		}
		l.tasks.mu.Unlock()
		return nil
	}
	l.tasks.mu.Unlock()
	l.transferMu.Lock()
	defer l.transferMu.Unlock()
	if incoming := l.tasks.incoming[frame.StreamID]; incoming != nil && incoming.cancelling && incoming.manifest.TransferID == result.TransferID && terminal {
		incoming.event.State = result.State
		incoming.event.Error = result.Error
		l.reportTransfer(incoming.event)
		delete(l.tasks.incoming, frame.StreamID)
	}
	return nil
}

func (l *Link) incomingResult(ctx context.Context, stream uint64, state *receivedTransfer, status string, err error) error {
	state.event.State = status
	if err != nil {
		state.event.Error = err.Error()
	}
	l.reportTransfer(state.event)
	result := fileResult{TransferID: state.manifest.TransferID, State: status, Bytes: state.event.Bytes, Total: state.event.Total, Error: state.event.Error}
	if status == "completed" || status == "failed" || status == "cancelled" {
		l.tasks.finished[stream] = result
		if len(l.tasks.finished) > 128 {
			for id := range l.tasks.finished {
				if id != stream {
					delete(l.tasks.finished, id)
					break
				}
			}
		}
	}
	return l.sendFileResult(ctx, stream, result)
}

func (l *Link) rejectIncoming(ctx context.Context, stream uint64, state *receivedTransfer, err error) error {
	state.rejected = true
	if state.receiver != nil {
		err = errors.Join(err, state.receiver.Cancel())
	}
	return l.incomingResult(ctx, stream, state, "failed", err)
}

func (l *Link) handleTrackedFileFrame(ctx context.Context, frame protocol.Frame) error {
	l.transferMu.Lock()
	defer l.transferMu.Unlock()
	state := l.tasks.incoming[frame.StreamID]
	if frame.Type == protocol.TypeFileCancel {
		var cancel fileCancel
		if len(frame.Payload) == 0 {
			if state != nil {
				cancel = fileCancel{TransferID: state.manifest.TransferID, Origin: "sender"}
			}
		} else if err := decodeStrictJSON(frame.Payload, &cancel); err != nil {
			return err
		}
		if cancel.Origin == "receiver" {
			l.tasks.mu.Lock()
			out := l.tasks.outgoing[frame.StreamID]
			if out != nil && out.event.ID == cancel.TransferID {
				out.peerCancelled.Store(true)
				out.cancel()
			}
			l.tasks.mu.Unlock()
			return nil
		}
		if cancel.Origin != "sender" {
			return errors.New("文件取消方向无效")
		}
		if state == nil {
			if result, exists := l.tasks.finished[frame.StreamID]; exists && result.TransferID == cancel.TransferID {
				return l.sendFileResult(ctx, frame.StreamID, result)
			}
			return l.sendFileResult(ctx, frame.StreamID, fileResult{TransferID: cancel.TransferID, State: "cancelled"})
		}
		if cancel.TransferID != state.manifest.TransferID {
			return errors.New("取消请求与文件任务不一致")
		}
		if state.receiver != nil {
			if err := state.receiver.Cancel(); err != nil {
				return l.incomingResult(ctx, frame.StreamID, state, "failed", err)
			}
		}
		state.rejected = true
		delete(l.tasks.incoming, frame.StreamID)
		return l.incomingResult(ctx, frame.StreamID, state, "cancelled", nil)
	}
	if state != nil && state.rejected {
		if frame.Type == protocol.TypeFileCommit && !state.cancelling {
			delete(l.tasks.incoming, frame.StreamID)
		}
		return nil
	}
	if l.transferRoot == "" {
		return errors.New("文件接收未配置")
	}
	switch frame.Type {
	case protocol.TypeFileManifest:
		if state == nil {
			if len(l.tasks.incoming) >= 8 {
				return errors.New("同时接收的文件任务过多")
			}
			var start struct {
				TransferID string    `json:"transfer_id"`
				CreatedAt  time.Time `json:"created_at"`
			}
			if err := decodeStrictJSON(frame.Payload, &start); err != nil {
				return err
			}
			if len(start.TransferID) == 0 || len(start.TransferID) > 64 {
				return errors.New("文件任务编号无效")
			}
			state = &receivedTransfer{manifest: transfer.Manifest{TransferID: start.TransferID, CreatedAt: start.CreatedAt}, event: TransferEvent{ID: start.TransferID, StreamID: frame.StreamID, Direction: "receive", State: "preparing", Name: "文件任务"}}
			l.tasks.incoming[frame.StreamID] = state
			if l.tasks.allow != nil && !l.tasks.allow() {
				return l.rejectIncoming(ctx, frame.StreamID, state, errors.New("这台电脑已暂停接收新文件"))
			}
			l.reportTransfer(state.event)
			return nil
		}
		if state.receiver != nil {
			return errors.New("文件清单已结束")
		}
		var entry transfer.Entry
		if err := decodeStrictJSON(frame.Payload, &entry); err != nil {
			return err
		}
		state.manifest.Entries = append(state.manifest.Entries, entry)
		return nil
	case protocol.TypeFileManifestEnd:
		if state == nil || state.receiver != nil || len(frame.Payload) != 0 {
			return errors.New("文件清单结束消息无效")
		}
		if err := state.manifest.Validate(); err != nil {
			return err
		}
		for _, entry := range state.manifest.Entries {
			if entry.Size > math.MaxInt64-state.event.Total {
				return errors.New("任务总大小溢出")
			}
			state.event.Total += entry.Size
		}
		state.event.Name = strings.SplitN(state.manifest.Entries[0].Path, "/", 2)[0]
		receiver, err := transfer.NewReceiver(l.transferRoot, state.manifest)
		if err != nil {
			return l.rejectIncoming(ctx, frame.StreamID, state, err)
		}
		state.receiver = receiver
		return l.incomingResult(ctx, frame.StreamID, state, "transferring", nil)
	case protocol.TypeFileChunk:
		if state == nil || state.receiver == nil {
			return errors.New("文件数据早于清单")
		}
		name, offset, data, err := decodeFileChunk(frame.Payload)
		if err != nil {
			return err
		}
		count, err := state.receiver.Write(name, offset, bytes.NewReader(data))
		if err != nil {
			return l.rejectIncoming(ctx, frame.StreamID, state, err)
		}
		state.event.Bytes += count
		if time.Since(state.lastProgress) >= 100*time.Millisecond || state.event.Bytes == state.event.Total {
			state.lastProgress = time.Now()
			return l.incomingResult(ctx, frame.StreamID, state, "transferring", nil)
		}
		return nil
	case protocol.TypeFileComplete:
		if state == nil || state.receiver == nil {
			return errors.New("文件完成消息无效")
		}
		if err := l.incomingResult(ctx, frame.StreamID, state, "verifying", nil); err != nil {
			return err
		}
		for _, entry := range state.manifest.Entries {
			if entry.Path == string(frame.Payload) && entry.Type == transfer.File && entry.Size == 0 {
				if _, err := state.receiver.Write(entry.Path, 0, bytes.NewReader(nil)); err != nil {
					return l.rejectIncoming(ctx, frame.StreamID, state, err)
				}
				break
			}
		}
		if err := state.receiver.CompleteFile(string(frame.Payload)); err != nil {
			return l.rejectIncoming(ctx, frame.StreamID, state, err)
		}
		return nil
	case protocol.TypeFileCommit:
		if state == nil || state.receiver == nil || len(frame.Payload) != 0 {
			return errors.New("文件提交消息无效")
		}
		if err := l.incomingResult(ctx, frame.StreamID, state, "verifying", nil); err != nil {
			return err
		}
		final, err := state.receiver.Commit()
		if err != nil {
			return l.rejectIncoming(ctx, frame.StreamID, state, err)
		}
		state.event.Path = final
		if err := l.incomingResult(ctx, frame.StreamID, state, "publishing", nil); err != nil {
			return err
		}
		if l.filesReady != nil {
			if err := l.filesReady(ctx, topLevelPaths(final, state.manifest)); err != nil {
				state.rejected = true
				return l.incomingResult(ctx, frame.StreamID, state, "failed", err)
			}
		}
		delete(l.tasks.incoming, frame.StreamID)
		return l.incomingResult(ctx, frame.StreamID, state, "completed", nil)
	}
	return errors.New("未知文件任务消息")
}
