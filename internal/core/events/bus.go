// Package events is the in-process event bus that feeds the UI over SSE and
// lets subsystems react to each other without direct coupling.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	ID    uint64    `json:"id"`
	Type  string    `json:"type"`
	Time  time.Time `json:"time"`
	Data  any       `json:"data"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
	seq  atomic.Uint64
	drop atomic.Uint64
}

func New() *Bus { return &Bus{subs: map[int]chan Event{}} }

// Publish never blocks; a subscriber that falls behind loses events (the UI
// re-fetches state on reconnect), which is counted for diagnostics.
func (b *Bus) Publish(typ string, data any) {
	e := Event{ID: b.seq.Add(1), Type: typ, Time: time.Now().UTC(), Data: data}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.drop.Add(1)
		}
	}
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

func (b *Bus) Dropped() uint64 { return b.drop.Load() }
