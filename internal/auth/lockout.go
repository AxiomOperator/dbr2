// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"sync"
	"time"
)

// Lockout policy for the master admin (final_stack → Authentication).
const (
	LockoutThreshold = 5
	lockoutBase      = time.Minute
	lockoutMax       = time.Hour
)

// LockDuration returns how long the account locks after `failures`
// consecutive failures: none below the threshold, then 1m, 2m, 4m … up to 1h.
func LockDuration(failures int) time.Duration {
	if failures < LockoutThreshold {
		return 0
	}
	d := lockoutBase << uint(min(failures-LockoutThreshold, 10))
	return min(d, lockoutMax)
}

// RateLimiter is a per-key fixed-window limiter for login attempts. It is
// in-memory (single dbr2-server instance in v1.0); a shared Valkey-backed
// limiter can replace it when the server scales out — losing its state only
// relaxes throttling, never correctness (ADR-0010).
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string]*window
}

type window struct {
	start time.Time
	count int
}

// NewRateLimiter allows `limit` attempts per `per` for each key.
func NewRateLimiter(limit int, per time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: per, now: time.Now, hits: map[string]*window{}}
}

// Allow records an attempt for key and reports whether it is permitted, plus
// the time until the window resets when it is not.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) > 10000 {
		for k, w := range l.hits {
			if now.Sub(w.start) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) >= l.window {
		l.hits[key] = &window{start: now, count: 1}
		return true, 0
	}
	if w.count >= l.limit {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.count++
	return true, 0
}
