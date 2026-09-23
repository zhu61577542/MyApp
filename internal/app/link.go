package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"time"

	"myapp/internal/clipboard"
	common "myapp/internal/input"
	"myapp/internal/protocol"
	"myapp/internal/transfer"
	"myapp/internal/transport"
)

type Link struct {
	writer       *transport.PriorityWriter
	bulkWriter   *transport.PriorityWriter
	injector     common.Injector
	clipboard    *clipboard.Synchronizer
	sequence     uint64
	mu           sync.Mutex
	transferMu   sync.Mutex
	transferRoot string
	incoming     map[uint64]*incomingTransfer
	filesReady   func(context.Context, []string) error
	textReceived func(context.Context, clipboard.Entry) error
	disconnected func(error)
}

// 仅在启动 Run 前设置。
func (l *Link) SetDisconnectHandler(handler func(error)) { l.disconnected = handler }

// 仅在启动 Run 前设置。
func (l *Link) SetTextReceiver(receive func(context.Context, clipboard.Entry) error) {
	l.textReceived = receive
}

type incomingTransfer struct {
	manifest transfer.Manifest
	receiver *transfer.Receiver
}

const fileChunkSize = 16 << 10

func NewLink(injector common.Injector, clipboardSync *clipboard.Synchronizer) (*Link, error) {
	writer, err := transport.NewPriorityWriter(1024, 16, protocol.DefaultMaxPayload)
	if err != nil {
		return nil, err
	}
	return &Link{writer: writer, injector: injector, clipboard: clipboardSync, incoming: make(map[uint64]*incomingTransfer)}, nil
}

func NewSplitLink(injector common.Injector, clipboardSync *clipboard.Synchronizer) (*Link, error) {
	link, err := NewLink(injector, clipboardSync)
	if err != nil {
		return nil, err
	}
	link.bulkWriter, err = transport.NewPriorityWriter(1, 16, protocol.DefaultMaxPayload)
	return link, err
}

func (l *Link) EnableTransfers(cacheRoot string, filesReady func(context.Context, []string) error) error {
	if cacheRoot == "" {
		return errors.New("文件缓存目录不能为空")
	}
	root, err := filepath.Abs(cacheRoot)
	if err != nil {
		return err
	}
	l.transferMu.Lock()
	l.transferRoot, l.filesReady = root, filesReady
	l.transferMu.Unlock()
	return nil
}

func (l *Link) SendInput(ctx context.Context, event common.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	var payload bytes.Buffer
	if err := event.Encode(&payload); err != nil {
		return err
	}
	return l.enqueue(ctx, transport.PriorityRealtime, protocol.TypeInput, payload.Bytes())
}

func (l *Link) SendReleaseAll(ctx context.Context) error {
	return l.enqueue(ctx, transport.PriorityRealtime, protocol.TypeReleaseAll, nil)
}

func (l *Link) PublishClipboard(ctx context.Context, entry clipboard.Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return l.enqueue(ctx, transport.PriorityBulk, protocol.TypeClipboard, payload)
}

func (l *Link) SendFiles(ctx context.Context, transferID string, sources []transfer.Source) error {
	manifest, err := transfer.BuildManifest(transferID, sources, time.Now())
	if err != nil {
		return err
	}
	streamID := l.nextStreamID()
	start, err := json.Marshal(struct {
		TransferID string    `json:"transfer_id"`
		CreatedAt  time.Time `json:"created_at"`
	}{manifest.TransferID, manifest.CreatedAt})
	if err != nil {
		return err
	}
	if err := l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileManifest, streamID, start); err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if err := l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileManifest, streamID, payload); err != nil {
			return err
		}
	}
	if err := l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileManifestEnd, streamID, nil); err != nil {
		return err
	}
	buffer := make([]byte, fileChunkSize)
	for _, entry := range manifest.Entries {
		if entry.Type != transfer.File {
			continue
		}
		file, err := transfer.OpenSource(sources, entry)
		if err != nil {
			return err
		}
		var offset int64
		for {
			count, readErr := file.Read(buffer)
			if count > 0 {
				payload, err := encodeFileChunk(entry.Path, offset, buffer[:count])
				if err != nil {
					_ = file.Close()
					return err
				}
				if err := l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileChunk, streamID, payload); err != nil {
					_ = file.Close()
					return err
				}
				offset += int64(count)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = file.Close()
				return readErr
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileComplete, streamID, []byte(entry.Path)); err != nil {
			return err
		}
	}
	return l.enqueueStream(ctx, transport.PriorityBulk, protocol.TypeFileCommit, streamID, nil)
}

func (l *Link) Run(ctx context.Context, connection io.ReadWriteCloser) error {
	return l.run(ctx, connection, nil)
}

func (l *Link) RunChannels(ctx context.Context, realtime, bulk io.ReadWriteCloser) error {
	if bulk == nil || l.bulkWriter == nil {
		return errors.New("独立传输通道未配置")
	}
	return l.run(ctx, realtime, bulk)
}

func (l *Link) run(ctx context.Context, connection, bulk io.ReadWriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	monitor, err := transport.NewHeartbeatMonitor(750*time.Millisecond, time.Now())
	if err != nil {
		return err
	}
	errorsChannel := make(chan error, 3)
	bulkErrors := make(chan error, 2)
	realtimeRemaining, bulkRemaining := 3, 0
	channel := transport.Channel("")
	if bulk != nil {
		channel = transport.ChannelRealtime
		bulkRemaining = 2
		go func() { bulkErrors <- l.bulkWriter.Run(ctx, bulk) }()
		go func() { bulkErrors <- l.readChannel(ctx, bulk, monitor, transport.ChannelBulk) }()
	}
	go func() { errorsChannel <- l.writer.Run(ctx, connection) }()
	go func() { errorsChannel <- l.readChannel(ctx, connection, monitor, channel) }()
	go func() {
		errorsChannel <- transport.RunHeartbeat(ctx, monitor, 100*time.Millisecond, func(frame protocol.Frame) error {
			return l.writer.Enqueue(ctx, transport.PriorityRealtime, frame)
		})
	}()
	select {
	case err = <-errorsChannel:
		realtimeRemaining--
	case err = <-bulkErrors:
		bulkRemaining--
	}
	if l.disconnected != nil {
		l.disconnected(err)
	}
	cancel()
	_ = connection.Close()
	if bulk != nil {
		_ = bulk.Close()
	}
	for range realtimeRemaining {
		<-errorsChannel
	}
	if l.injector != nil {
		err = errors.Join(err, l.injector.ReleaseAll())
	}
	for range bulkRemaining {
		<-bulkErrors
	}
	return err
}

func (l *Link) read(ctx context.Context, connection io.Reader, monitor *transport.HeartbeatMonitor) error {
	return l.readChannel(ctx, connection, monitor, "")
}

func (l *Link) readChannel(ctx context.Context, connection io.Reader, monitor *transport.HeartbeatMonitor, channel transport.Channel) error {
	decoder := protocol.Decoder{MaxPayload: protocol.DefaultMaxPayload}
	var lastInputSequence uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := decoder.Decode(connection)
		if err != nil {
			return err
		}
		realtime := frame.Type == protocol.TypeInput || frame.Type == protocol.TypeReleaseAll || frame.Type == protocol.TypeHeartbeat
		if channel == transport.ChannelRealtime && !realtime || channel == transport.ChannelBulk && realtime {
			return errors.New("消息类型与会话通道不匹配")
		}
		switch frame.Type {
		case protocol.TypeHeartbeat:
			if _, err := transport.ParseHeartbeat(frame); err != nil {
				return err
			}
			monitor.Observe(time.Now())
		case protocol.TypeInput:
			if len(frame.Payload) != common.WireSize {
				return errors.New("输入事件长度无效")
			}
			if l.injector == nil {
				return errors.New("本机会话不接受输入注入")
			}
			event, err := common.Decode(bytes.NewReader(frame.Payload))
			if err != nil {
				return err
			}
			if event.Sequence <= lastInputSequence {
				return errors.New("输入事件重复或序号倒退")
			}
			lastInputSequence = event.Sequence
			if err := l.injector.Inject(event); err != nil {
				return err
			}
		case protocol.TypeReleaseAll:
			if len(frame.Payload) != 0 || l.injector == nil {
				return errors.New("释放全部输入消息无效")
			}
			if err := l.injector.ReleaseAll(); err != nil {
				return err
			}
		case protocol.TypeClipboard:
			if l.clipboard == nil && l.textReceived == nil {
				return errors.New("本机会话未启用剪贴板")
			}
			var entry clipboard.Entry
			decoder := json.NewDecoder(bytes.NewReader(frame.Payload))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&entry); err != nil {
				return err
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				return errors.New("剪贴板消息包含多余数据")
			}
			if err := entry.Validate(); err != nil {
				return err
			}
			if l.textReceived != nil {
				if err := l.textReceived(ctx, entry); err != nil {
					return err
				}
			} else {
				if _, err := l.clipboard.Apply(ctx, entry); err != nil {
					return err
				}
			}
		case protocol.TypeFileManifest, protocol.TypeFileManifestEnd, protocol.TypeFileChunk, protocol.TypeFileComplete, protocol.TypeFileCommit, protocol.TypeFileCancel:
			if err := l.handleFileFrame(ctx, frame); err != nil {
				return err
			}
		default:
			if frame.Flags&protocol.FlagCritical != 0 {
				return protocol.ErrUnknownMessage
			}
		}
	}
}

func (l *Link) enqueue(ctx context.Context, priority transport.Priority, messageType protocol.MessageType, payload []byte) error {
	return l.enqueueStream(ctx, priority, messageType, l.nextStreamID(), payload)
}

func (l *Link) nextStreamID() uint64 {
	l.mu.Lock()
	l.sequence++
	streamID := l.sequence
	l.mu.Unlock()
	return streamID
}

func (l *Link) enqueueStream(ctx context.Context, priority transport.Priority, messageType protocol.MessageType, streamID uint64, payload []byte) error {
	if priority == transport.PriorityBulk && l.bulkWriter != nil {
		return l.bulkWriter.Enqueue(ctx, priority, protocol.Frame{Type: messageType, Flags: protocol.FlagCritical, StreamID: streamID, Payload: payload})
	}
	return l.writer.Enqueue(ctx, priority, protocol.Frame{Type: messageType, Flags: protocol.FlagCritical, StreamID: streamID, Payload: payload})
}

func (l *Link) handleFileFrame(ctx context.Context, frame protocol.Frame) error {
	l.transferMu.Lock()
	defer l.transferMu.Unlock()
	if l.transferRoot == "" {
		return errors.New("本机会话未启用文件接收")
	}
	state := l.incoming[frame.StreamID]
	switch frame.Type {
	case protocol.TypeFileManifest:
		if state == nil {
			var start struct {
				TransferID string    `json:"transfer_id"`
				CreatedAt  time.Time `json:"created_at"`
			}
			if err := decodeStrictJSON(frame.Payload, &start); err != nil {
				return err
			}
			l.incoming[frame.StreamID] = &incomingTransfer{manifest: transfer.Manifest{TransferID: start.TransferID, CreatedAt: start.CreatedAt}}
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
		receiver, err := transfer.NewReceiver(l.transferRoot, state.manifest)
		if err != nil {
			return err
		}
		state.receiver = receiver
		return nil
	case protocol.TypeFileChunk:
		if state == nil || state.receiver == nil {
			return errors.New("文件数据早于清单")
		}
		path, offset, data, err := decodeFileChunk(frame.Payload)
		if err != nil {
			return err
		}
		_, err = state.receiver.Write(path, offset, bytes.NewReader(data))
		return err
	case protocol.TypeFileComplete:
		if state == nil || state.receiver == nil {
			return errors.New("文件完成消息无效")
		}
		for _, entry := range state.manifest.Entries {
			if entry.Path == string(frame.Payload) && entry.Type == transfer.File && entry.Size == 0 {
				if _, err := state.receiver.Write(entry.Path, 0, bytes.NewReader(nil)); err != nil {
					return err
				}
				break
			}
		}
		return state.receiver.CompleteFile(string(frame.Payload))
	case protocol.TypeFileCommit:
		if state == nil || state.receiver == nil || len(frame.Payload) != 0 {
			return errors.New("文件提交消息无效")
		}
		final, err := state.receiver.Commit()
		if err != nil {
			return err
		}
		delete(l.incoming, frame.StreamID)
		if l.filesReady != nil {
			paths := topLevelPaths(final, state.manifest)
			return l.filesReady(ctx, paths)
		}
		return nil
	case protocol.TypeFileCancel:
		if state != nil && state.receiver != nil {
			_ = state.receiver.Cancel()
		}
		delete(l.incoming, frame.StreamID)
		return nil
	default:
		return errors.New("未知文件消息")
	}
}

func encodeFileChunk(path string, offset int64, data []byte) ([]byte, error) {
	if err := transfer.ValidateRelativePath(path); err != nil || len(path) > 65535 || offset < 0 {
		return nil, errors.New("文件块头无效")
	}
	payload := make([]byte, 10+len(path)+len(data))
	payload[0], payload[1] = byte(len(path)>>8), byte(len(path))
	for index := 0; index < 8; index++ {
		payload[2+index] = byte(uint64(offset) >> (56 - index*8))
	}
	copy(payload[10:], path)
	copy(payload[10+len(path):], data)
	return payload, nil
}

func decodeFileChunk(payload []byte) (string, int64, []byte, error) {
	if len(payload) < 10 {
		return "", 0, nil, errors.New("文件块过短")
	}
	pathLength := int(payload[0])<<8 | int(payload[1])
	if pathLength == 0 || 10+pathLength > len(payload) {
		return "", 0, nil, errors.New("文件块路径长度无效")
	}
	var offset uint64
	for index := 0; index < 8; index++ {
		offset = offset<<8 | uint64(payload[2+index])
	}
	if offset > uint64(^uint64(0)>>1) {
		return "", 0, nil, errors.New("文件块偏移无效")
	}
	path := string(payload[10 : 10+pathLength])
	if err := transfer.ValidateRelativePath(path); err != nil {
		return "", 0, nil, err
	}
	return path, int64(offset), payload[10+pathLength:], nil
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON 消息包含多余数据")
	}
	return nil
}

func topLevelPaths(root string, manifest transfer.Manifest) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, entry := range manifest.Entries {
		first := entry.Path
		if index := bytes.IndexByte([]byte(first), '/'); index >= 0 {
			first = first[:index]
		}
		if _, exists := seen[first]; !exists {
			seen[first] = struct{}{}
			result = append(result, filepath.Join(root, filepath.FromSlash(first)))
		}
	}
	return result
}
