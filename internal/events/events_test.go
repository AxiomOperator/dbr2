// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

func TestFanOutAndUnsubscribe(t *testing.T) {
	b := NewBus(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	a, c := b.Subscribe(ctx), b.Subscribe(context.Background())
	b.Publish(ctx, New(AgentStatus, rbac.HostRead, map[string]any{"host_id": "h1", "connected": true}))
	for _, ch := range []<-chan Event{a, c} {
		select {
		case e := <-ch:
			if e.Type != AgentStatus || string(e.Data) != `{"connected":true,"host_id":"h1"}` {
				t.Fatalf("event %+v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("no event")
		}
	}
	cancel()
	if _, ok := <-a; ok {
		t.Fatal("channel not closed after cancel")
	}
	if n := b.Subscribers(); n != 1 {
		t.Fatalf("subscribers = %d", n)
	}
}

func TestSlowSubscriberDrops(t *testing.T) {
	b := NewBus(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Subscribe(context.Background()) // never read
	done := make(chan struct{})
	go func() {
		for range bufferSize * 3 {
			b.Publish(context.Background(), New(JobProgress, rbac.BackupRead, nil))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
}
