package app

import (
	"context"
	"errors"
	"sync"

	"myapp/internal/clipboard"
	"myapp/internal/device"
	common "myapp/internal/input"
	"myapp/internal/transfer"
)

type delivery struct {
	text       *clipboard.Entry
	transferID string
	paths      []string
}

type hubPeer struct {
	id    string
	link  *Link
	queue chan delivery
}

type Hub struct {
	mu      sync.Mutex
	inputMu sync.Mutex
	manager *device.Manager
	peers   []*hubPeer
}

func NewHub(local device.Device, maxDevices int) (*Hub, error) {
	manager, err := device.NewManager(local, maxDevices)
	if err != nil {
		return nil, err
	}
	return &Hub{manager: manager}, nil
}

func (h *Hub) AddPeer(peer device.Device, link *Link) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if link == nil {
		return errors.New("目标连接不能为空")
	}
	if err := h.manager.AddPeer(peer); err != nil {
		return err
	}
	h.peers = append(h.peers, &hubPeer{id: peer.ID, link: link, queue: make(chan delivery, 8)})
	return nil
}

func (h *Hub) snapshot() []*hubPeer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*hubPeer(nil), h.peers...)
}

func (h *Hub) Activate(ctx context.Context, index int) error {
	h.inputMu.Lock()
	defer h.inputMu.Unlock()
	peers := h.snapshot()
	if index < 0 || index >= len(peers) {
		return device.ErrUnknownDevice
	}
	old, exists := h.manager.Active()
	if exists && old.ID == peers[index].id {
		return nil
	}
	if exists {
		for _, peer := range peers {
			if peer.id == old.ID {
				if err := peer.link.SendReleaseAll(ctx); err != nil {
					h.manager.RecoverLocal()
					return err
				}
			}
		}
	}
	return h.manager.Activate(peers[index].id)
}

func (h *Hub) SendInput(ctx context.Context, event common.Event) error {
	h.inputMu.Lock()
	defer h.inputMu.Unlock()
	active, exists := h.manager.Active()
	if !exists {
		return device.ErrTargetOffline
	}
	for _, peer := range h.snapshot() {
		if peer.id == active.ID {
			return peer.link.SendInput(ctx, event)
		}
	}
	return device.ErrUnknownDevice
}

func (h *Hub) PublishText(ctx context.Context, source string, entry clipboard.Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	return h.broadcast(ctx, source, delivery{text: &entry})
}

func (h *Hub) PublishFiles(ctx context.Context, source, transferID string, paths []string) error {
	return h.broadcast(ctx, source, delivery{transferID: transferID, paths: append([]string(nil), paths...)})
}

func (h *Hub) broadcast(ctx context.Context, source string, item delivery) error {
	for _, peer := range h.snapshot() {
		if peer.id == source {
			continue
		}
		select {
		case peer.queue <- item:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (h *Hub) RunRelay(ctx context.Context, peerID string) error {
	var target *hubPeer
	for _, peer := range h.snapshot() {
		if peer.id == peerID {
			target = peer
			break
		}
	}
	if target == nil {
		return device.ErrUnknownDevice
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item := <-target.queue:
			var err error
			if item.text != nil {
				err = target.link.PublishClipboard(ctx, *item.text)
			} else {
				sources := make([]transfer.Source, len(item.paths))
				for i, path := range item.paths {
					sources[i] = transfer.Source{Path: path}
				}
				err = target.link.SendFiles(ctx, item.transferID, sources)
			}
			if err != nil {
				return err
			}
		}
	}
}
