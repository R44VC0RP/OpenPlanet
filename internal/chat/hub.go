package chat

import (
	"sync"

	"gamegateway/internal/store"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[chan store.ChatMessage]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[chan store.ChatMessage]struct{})}
}

func (h *Hub) Subscribe(roomID string) (<-chan store.ChatMessage, func()) {
	ch := make(chan store.ChatMessage, 16)
	h.mu.Lock()
	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[chan store.ChatMessage]struct{})
	}
	h.rooms[roomID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if subscribers, ok := h.rooms[roomID]; ok {
			delete(subscribers, ch)
			close(ch)
			if len(subscribers) == 0 {
				delete(h.rooms, roomID)
			}
		}
	}
}

func (h *Hub) Publish(msg store.ChatMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.rooms[msg.RoomID] {
		select {
		case ch <- msg:
		default:
		}
	}
}
