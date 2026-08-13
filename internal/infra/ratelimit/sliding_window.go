// Package ratelimit implements the domain rate limiter port with an in-process
// sliding window.
//
// State is per process: with several facilitator replicas behind a load
// balancer each replica enforces its own quota. Swap this adapter for one
// backed by a shared store (Redis) if you need a cluster-wide limit.
package ratelimit

import (
	"sync"
	"time"

	domain "github.com/islu/x402-facilitator/internal/domain/ratelimit"
)

// SlidingWindow allows at most limit events per window for each key.
// The zero value is not usable; call New.
type SlidingWindow struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
}

var _ domain.Limiter = (*SlidingWindow)(nil)

// New creates a limiter allowing limit events per window.
// A limit of 0 or less disables limiting: Allow always returns true.
func New(limit int, window time.Duration) *SlidingWindow {
	return &SlidingWindow{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   map[string][]time.Time{},
	}
}

// Enabled reports whether the limiter enforces anything.
func (l *SlidingWindow) Enabled() bool {
	return l != nil && l.limit > 0
}

// Allow records an event for key and reports whether it is within quota.
// An empty key is always allowed — callers that cannot identify a payer should
// not be blocked by another payer's quota.
func (l *SlidingWindow) Allow(key string) bool {
	if !l.Enabled() || key == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, hit := range l.hits[key] {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}

	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}

	l.hits[key] = append(kept, now)
	return true
}

// Cleanup drops keys whose events have all aged out. Run it periodically so
// one-shot payers do not accumulate in memory.
func (l *SlidingWindow) Cleanup() {
	if !l.Enabled() {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.window)
	for key, hits := range l.hits {
		live := hits[:0]
		for _, hit := range hits {
			if hit.After(cutoff) {
				live = append(live, hit)
			}
		}
		if len(live) == 0 {
			delete(l.hits, key)
			continue
		}
		l.hits[key] = live
	}
}

// RunCleanup runs Cleanup every interval until done is closed.
func (l *SlidingWindow) RunCleanup(interval time.Duration, done <-chan struct{}) {
	if !l.Enabled() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			l.Cleanup()
		}
	}
}
