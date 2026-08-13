// Package ratelimit defines the port used to cap how often a single payer can
// ask the facilitator to do work.
package ratelimit

//go:generate go tool mockgen -destination=../../mocks/domain/limiter.go -package=mocks -source=limiter.go

// Limiter decides whether an action for key is within quota, recording the
// attempt as it goes.
//
// Implementations live under internal/infra/ratelimit. Enabled lets callers
// skip the bookkeeping (and the log line) entirely when no limit is configured.
type Limiter interface {
	// Allow records an attempt for key and reports whether it is within quota.
	Allow(key string) bool
	// Enabled reports whether the limiter enforces anything.
	Enabled() bool
}
