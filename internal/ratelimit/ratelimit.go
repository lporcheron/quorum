// Package ratelimit is a small in-memory fixed-window limiter, one
// counter per key (typically a client IP). No external store: a
// single-binary instance rate-limits itself.
package ratelimit

import (
	"sync"
	"time"
)

// maxKeys caps the number of tracked keys. Pruning only drops expired
// windows, so a flood of distinct keys inside one window could
// otherwise grow the map without bound; past the cap, arbitrary live
// entries are evicted. An evicted key just gets a fresh window: memory
// stays bounded at the price of a little leniency under attack.
const maxKeys = 100_000

// Limiter allows max events per window and key.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastPrune time.Time
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
		// A new key doubles as cleanup opportunity: prune stale entries,
		// at most once a minute — a full scan on every new key would
		// turn a flood of distinct keys into quadratic CPU work.
		if len(l.buckets) > 4096 && now.Sub(l.lastPrune) >= time.Minute {
			l.lastPrune = now
			for k, old := range l.buckets {
				if now.Sub(old.start) >= l.window {
					delete(l.buckets, k)
				}
			}
		}
		if len(l.buckets) >= maxKeys {
			// Evict a tenth in one pass (map order is random), so the
			// cost is paid once per many insertions, not on each.
			n := maxKeys / 10
			for k := range l.buckets {
				if n == 0 {
					break
				}
				delete(l.buckets, k)
				n--
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
