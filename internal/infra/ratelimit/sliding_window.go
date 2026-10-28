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

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastPrune time.Time
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
	// ponytail: a full sweep once per window under the lock, so one-shot keys
	// do not accumulate without a cleanup goroutine per limiter. Shard the map
	// if the key count ever makes this pause visible.
	if now.Sub(l.lastPrune) >= l.window {
		l.pruneLocked(now)
	}
	kept := l.liveLocked(key, now)
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// Exceeded reports whether key has used up its quota, without recording an
// event.
func (l *SlidingWindow) Exceeded(key string) bool {
	if !l.Enabled() || key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.liveLocked(key, l.now())) >= l.limit
}

// Cleanup drops keys whose events have all aged out. Allow runs it once per
// window, so callers only need it to force a sweep.
func (l *SlidingWindow) Cleanup() {
	if !l.Enabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.now())
}

// liveLocked returns the events for key still inside the window. Callers must
// hold l.mu.
func (l *SlidingWindow) liveLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, hit := range l.hits[key] {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	return kept
}

// pruneLocked drops every key with no live events. Callers must hold l.mu.
func (l *SlidingWindow) pruneLocked(now time.Time) {
	l.lastPrune = now
	for key := range l.hits {
		if live := l.liveLocked(key, now); len(live) > 0 {
			l.hits[key] = live
		} else {
			delete(l.hits, key)
		}
	}
}
