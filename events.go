package main

import (
	"encoding/json"
	"sync"
)

// EventHub 向所有 SSE 订阅者广播事件，替代 Wails 的 EventsEmit。
type EventHub struct {
	mu   sync.RWMutex
	subs map[chan []byte]struct{}
}

// NewEventHub 创建事件中心。
func NewEventHub() *EventHub {
	return &EventHub{subs: make(map[chan []byte]struct{})}
}

// Subscribe 注册一个订阅者，返回接收消息的 channel。
func (h *EventHub) Subscribe() chan []byte {
	ch := make(chan []byte, 128)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 注销订阅者并关闭 channel。
func (h *EventHub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Emit 把事件序列化后广播给所有订阅者。慢消费者会丢消息而不是阻塞测速。
func (h *EventHub) Emit(event string, data any) {
	payload, err := json.Marshal(map[string]any{"event": event, "data": data})
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- payload:
		default:
		}
	}
}
