// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"sync"
)

// defaultMaxJobs applies when the gateway sends max_concurrent_jobs = 0.
const defaultMaxJobs = 2

// limiter is a counting semaphore whose size the gateway can change.
type limiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	changed chan struct{} // closed on every release or resize
}

func newLimiter(n int) *limiter { return &limiter{limit: n, changed: make(chan struct{})} }

func (l *limiter) setLimit(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n < 1 {
		n = 1
	}
	l.limit = n
	l.notify()
}

func (l *limiter) notify() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// tryAcquire takes a slot if one is free.
func (l *limiter) tryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active < l.limit {
		l.active++
		return true
	}
	return false
}

// acquire waits for a slot.
func (l *limiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		ch := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

func (l *limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.notify()
}
