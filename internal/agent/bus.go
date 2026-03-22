package agent

import (
	"sync"
	"time"

	"github.com/patricksign/AgentClaw/internal/adapter"
)

// EventBus is a simple in-process pub/sub using channels.
// All components subscribe to receive events (WebSocket, metrics, logger...).
type EventBus struct {
	mu   sync.RWMutex
	subs map[string]chan adapter.Event
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[string]chan adapter.Event),
	}
}

// Subscribe registers a receiver — returns a channel and an unsubscribe func.
// The caller MUST call unsub when done to avoid goroutine/channel leaks.
func (b *EventBus) Subscribe(id string) (<-chan adapter.Event, func()) {
	ch := make(chan adapter.Event, 64)
	b.mu.Lock()
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			// Hold the write lock for the entire delete+close sequence so that
			// no concurrent Publish can write to ch between delete and close.
			b.mu.Lock()
			delete(b.subs, id)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, unsub
}

// Publish sends an event to all current subscribers.
// Non-blocking: slow subscribers are dropped, not blocked.
func (b *EventBus) Publish(evt adapter.Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
			// slow subscriber — drop, do not block
		}
	}
}
