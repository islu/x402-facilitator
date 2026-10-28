package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attempt is one call to Allow at a point in time, relative to the limiter's
// creation, together with the verdict it should get.
type attempt struct {
	at      time.Duration
	key     string
	wantOK  bool
	comment string
}

func Test_Allow(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		window   time.Duration
		attempts []attempt
	}{
		{
			name:   "requests within the quota are allowed",
			limit:  2,
			window: time.Minute,
			attempts: []attempt{
				{key: "0xabc", wantOK: true},
				{key: "0xabc", wantOK: true},
				{key: "0xabc", wantOK: false, comment: "third exceeds the quota"},
			},
		},
		{
			name:   "quotas are tracked per key",
			limit:  1,
			window: time.Minute,
			attempts: []attempt{
				{key: "0xabc", wantOK: true},
				{key: "0xdef", wantOK: true, comment: "a different payer has its own quota"},
				{key: "0xabc", wantOK: false},
			},
		},
		{
			name:   "the window slides",
			limit:  1,
			window: time.Minute,
			attempts: []attempt{
				{key: "0xabc", wantOK: true},
				{at: 30 * time.Second, key: "0xabc", wantOK: false},
				{at: 61 * time.Second, key: "0xabc", wantOK: true, comment: "the first attempt aged out"},
			},
		},
		{
			// Otherwise a client hammering the endpoint would keep pushing its
			// own window forward and lock itself out indefinitely.
			name:   "a rejected attempt does not extend the window",
			limit:  1,
			window: time.Minute,
			attempts: []attempt{
				{key: "0xabc", wantOK: true},
				{at: 30 * time.Second, key: "0xabc", wantOK: false},
				{at: 61 * time.Second, key: "0xabc", wantOK: true, comment: "61s after the accepted attempt"},
			},
		},
		{
			name:   "a limit of zero disables the limiter",
			limit:  0,
			window: time.Minute,
			attempts: []attempt{
				{key: "0xabc", wantOK: true},
				{key: "0xabc", wantOK: true},
				{key: "0xabc", wantOK: true},
			},
		},
		{
			// Solana payloads carry an opaque transaction, so no payer is
			// readable. Those requests must not share a single bucket.
			name:   "an empty key is always allowed",
			limit:  1,
			window: time.Minute,
			attempts: []attempt{
				{key: "", wantOK: true},
				{key: "", wantOK: true},
				{key: "", wantOK: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			start := now
			limiter := New(tt.limit, tt.window)
			limiter.now = func() time.Time { return now }

			for i, a := range tt.attempts {
				now = start.Add(a.at)

				got := limiter.Allow(a.key)

				assert.Equal(t, a.wantOK, got, "attempt %d (%s): %s", i, a.at, a.comment)
			}
		})
	}
}

func Test_Enabled(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  bool
	}{
		{name: "a positive limit enforces", limit: 1, want: true},
		{name: "a zero limit disables", limit: 0, want: false},
		{name: "a negative limit disables", limit: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, New(tt.limit, time.Minute).Enabled())
		})
	}
}

func Test_Cleanup(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		elapsed      time.Duration
		wantRetained int
	}{
		{
			name:         "keys whose attempts aged out are dropped",
			limit:        5,
			elapsed:      2 * time.Minute,
			wantRetained: 0,
		},
		{
			name:         "keys still inside the window are kept",
			limit:        5,
			elapsed:      30 * time.Second,
			wantRetained: 1,
		},
		{
			name:         "a disabled limiter has nothing to clean",
			limit:        0,
			elapsed:      2 * time.Minute,
			wantRetained: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			limiter := New(tt.limit, time.Minute)
			limiter.now = func() time.Time { return now }
			require.True(t, limiter.Allow("0xabc"))

			now = now.Add(tt.elapsed)
			limiter.Cleanup()

			limiter.mu.Lock()
			defer limiter.mu.Unlock()
			assert.Len(t, limiter.hits, tt.wantRetained)
		})
	}
}

// Exceeded lets a caller check a quota before doing work and count the attempt
// only once it has proven itself, so it must never record anything.
func Test_Exceeded(t *testing.T) {
	limiter := New(1, time.Minute)

	assert.False(t, limiter.Exceeded("0xabc"))
	assert.False(t, limiter.Exceeded("0xabc"), "checking twice records nothing")
	require.True(t, limiter.Allow("0xabc"))
	assert.True(t, limiter.Exceeded("0xabc"))
	assert.False(t, limiter.Exceeded(""), "an empty key is never limited")
}

// One-shot keys must not pile up when nothing calls Cleanup.
func Test_Allow_PrunesAgedOutKeys(t *testing.T) {
	now := time.Now()
	limiter := New(5, time.Minute)
	limiter.now = func() time.Time { return now }
	require.True(t, limiter.Allow("0xabc"))

	now = now.Add(2 * time.Minute)
	require.True(t, limiter.Allow("0xdef"))

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	assert.NotContains(t, limiter.hits, "0xabc")
}
