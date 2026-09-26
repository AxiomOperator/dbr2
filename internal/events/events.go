// SPDX-License-Identifier: Apache-2.0

// Package events is the live-update bus behind the SSE endpoint (final
// stack → Live Browser Updates). Events are disposable notifications: a lost
// event only delays a refresh, because the console re-reads state from the
// API. With Valkey configured, events fan out across dbr2-server instances
// through Valkey pub/sub (never a system of record; ADR-0010); otherwise they
// stay in process.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// Event types (the SSE "event" field).
const (
	JobProgress    = "job.progress"
	BackupUpdated  = "backup.updated"
	RestoreUpdated = "restore.updated"
	AgentStatus    = "agent.status"
	AlertCreated   = "alert.created"
	InventoryUpd   = "inventory.updated"
)

// Event is one live update.
type Event struct {
	Type string          `json:"type"`
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
	// Permission a subscriber needs to receive it.
	Permission rbac.Permission `json:"permission"`
}

// New builds an event from a payload.
func New(typ string, perm rbac.Permission, data any) Event {
	b, _ := json.Marshal(data)
	return Event{Type: typ, At: time.Now().UTC(), Data: b, Permission: perm}
}

// Bus fans events out to subscribers.
type Bus struct {
	log     *slog.Logger
	valkey  valkey.Client
	channel string

	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// Subscriber buffer: a slow browser drops events instead of blocking.
const bufferSize = 64

// NewBus returns an in-process bus.
func NewBus(log *slog.Logger) *Bus {
	return &Bus{log: log, subs: map[chan Event]struct{}{}, channel: "dbr2:events"}
}

// UseValkey fans events out through Valkey pub/sub (multi-instance). It
// subscribes in the background and falls back to local delivery whenever
// publishing fails.
func (b *Bus) UseValkey(ctx context.Context, c valkey.Client) {
	b.valkey = c
	go func() {
		for ctx.Err() == nil {
			err := c.Receive(ctx, c.B().Subscribe().Channel(b.channel).Build(), func(m valkey.PubSubMessage) {
				var e Event
				if json.Unmarshal([]byte(m.Message), &e) == nil {
					b.deliver(e)
				}
			})
			if ctx.Err() != nil {
				return
			}
			b.log.Warn("valkey event subscription ended; retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// Publish sends an event to every subscriber (every instance with Valkey).
func (b *Bus) Publish(ctx context.Context, e Event) {
	if b == nil {
		return
	}
	if b.valkey != nil {
		payload, _ := json.Marshal(e)
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := b.valkey.Do(pctx, b.valkey.B().Publish().Channel(b.channel).Message(string(payload)).Build()).Error(); err == nil {
			return
		}
	}
	b.deliver(e)
}

func (b *Bus) deliver(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop (the console re-reads state)
		}
	}
}

// Subscribe returns a channel of events until ctx ends.
func (b *Bus) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, bufferSize)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}

// Subscribers reports the number of live subscribers (metrics, tests).
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
