// Package ratelimit is a small in-memory fixed-window limiter, one
// counter per key (typically a client IP). No external store: a
// single-binary instance rate-limits itself.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows max events per window and key.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	count int
	start time.Time
}

// New builds a limiter; now is injectable for tests.
func New(max int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{max: max, window: window, now: now, buckets: make(map[string]*bucket)}
}

// Allow consumes one slot for key and reports whether it fit in the
// current window.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		// Window rollover doubles as cleanup opportunity: prune stale
		// entries occasionally so the map cannot grow without bound.
		if len(l.buckets) > 4096 {
			for k, old := range l.buckets {
				if now.Sub(old.start) >= l.window {
					delete(l.buckets, k)
				}
			}
		}
		l.buckets[key] = &bucket{count: 1, start: now}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}
