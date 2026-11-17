// Package ratelimit defines the port used to cap how often a single payer or
// caller can ask the facilitator to do work.
package ratelimit

//go:generate go tool mockgen -destination=../../mocks/domain/limiter.go -package=mocks -source=limiter.go

// Limiter decides whether an action for key is within quota.
//
// Implementations live under internal/infra/ratelimit. A limiter with no limit
// configured allows everything and is never exceeded.
type Limiter interface {
	// Allow records an attempt for key and reports whether it is within quota.
	Allow(key string) bool
	// Exceeded reports whether key has used up its quota, without recording
	// anything. Callers that may only count an attempt once it has proven
	// itself check with Exceeded first and record with Allow afterwards.
	Exceeded(key string) bool
}

// ClientLimiter is the Limiter keyed by caller IP rather than by payer. It is
// a distinct type so the two quotas can be configured and injected
// independently.
type ClientLimiter Limiter
