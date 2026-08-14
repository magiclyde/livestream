package sfu

import (
	"log"
	"sync"
)

// Hub 管理全部房间，是 SFU 的顶层入口。
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
	logf  func(format string, args ...any)
}

// NewHub 构造 Hub；logf 为空时退化为标准库 log。
func NewHub(logf func(format string, args ...any)) *Hub {
	if logf == nil {
		logf = log.Printf
	}
	return &Hub{rooms: make(map[string]*Room), logf: logf}
}

func (h *Hub) Logf(format string, args ...any) { h.logf(format, args...) }

// GetOrCreateRoom 按房间号取房间，不存在则创建。
func (h *Hub) GetOrCreateRoom(id string) *Room {
	h.mu.RLock()
	r := h.rooms[id]
	h.mu.RUnlock()
	if r != nil {
		return r
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if r = h.rooms[id]; r == nil {
		r = newRoom(id, h)
		h.rooms[id] = r
	}
	return r
}

// removeRoom 在房间清空时删除房间，释放内存。
func (h *Hub) removeRoom(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[id]; ok && r.isEmpty() {
		delete(h.rooms, id)
	}
}
