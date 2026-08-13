package telemetry

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

func render(t *testing.T, registry *Registry) string {
	t.Helper()
	var out strings.Builder
	require.NoError(t, registry.Write(&out))
	return out.String()
}

func Test_Write(t *testing.T) {
	tests := []struct {
		name    string
		record  func(*Registry)
		want    []string
		wantNot []string
	}{
		{
			name: "counters accumulate per label set",
			record: func(r *Registry) {
				r.Describe("settlements_total", "Settlements.")
				r.Inc("settlements_total", telemetry.Labels{"network": "eip155:8453"})
				r.Inc("settlements_total", telemetry.Labels{"network": "eip155:8453"})
				r.Inc("settlements_total", telemetry.Labels{"network": "eip155:84532"})
			},
			want: []string{
				"# HELP settlements_total Settlements.",
				"# TYPE settlements_total counter",
				`settlements_total{network="eip155:8453"} 2`,
				`settlements_total{network="eip155:84532"} 1`,
			},
		},
		{
			// Label order must not fork one logical metric into two series.
			name: "label order does not split a series",
			record: func(r *Registry) {
				r.Inc("requests_total", telemetry.Labels{"a": "1", "b": "2"})
				r.Inc("requests_total", telemetry.Labels{"b": "2", "a": "1"})
			},
			want: []string{`requests_total{a="1",b="2"} 2`},
		},
		{
			name: "a gauge keeps only its latest value",
			record: func(r *Registry) {
				r.SetGauge("wallet_balance", telemetry.Labels{"network": "eip155:8453"}, 1.5)
				r.SetGauge("wallet_balance", telemetry.Labels{"network": "eip155:8453"}, 0.25)
			},
			want:    []string{`wallet_balance{network="eip155:8453"} 0.25`},
			wantNot: []string{"1.5"},
		},
		{
			name: "histogram buckets are cumulative",
			record: func(r *Registry) {
				r.Observe("settle_duration_seconds", nil, 300*time.Millisecond)
				r.Observe("settle_duration_seconds", nil, 2*time.Second)
			},
			want: []string{
				"# TYPE settle_duration_seconds histogram",
				`settle_duration_seconds_bucket{le="0.25"} 0`,
				`settle_duration_seconds_bucket{le="0.5"} 1`,
				`settle_duration_seconds_bucket{le="2.5"} 2`,
				`settle_duration_seconds_bucket{le="+Inf"} 2`,
				"settle_duration_seconds_count 2",
			},
		},
		{
			// Settlement waits on block inclusion, so the tail has to reach far
			// enough to distinguish "slow" from "stuck".
			name: "an observation past the last bucket lands in +Inf only",
			record: func(r *Registry) {
				r.Observe("settle_duration_seconds", nil, 120*time.Second)
			},
			want: []string{
				`settle_duration_seconds_bucket{le="60"} 0`,
				`settle_duration_seconds_bucket{le="+Inf"} 1`,
			},
		},
		{
			name: "label values are escaped",
			record: func(r *Registry) {
				r.Inc("errors_total", telemetry.Labels{"reason": `bad "quote"`})
			},
			want: []string{`errors_total{reason="bad \"quote\""} 1`},
		},
		{
			name:   "an empty registry renders nothing",
			record: func(*Registry) {},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := New()
			tt.record(registry)

			output := render(t, registry)

			if tt.want == nil {
				assert.Empty(t, output)
				return
			}
			for _, want := range tt.want {
				assert.Contains(t, output, want)
			}
			for _, unwanted := range tt.wantNot {
				assert.NotContains(t, output, unwanted)
			}
		})
	}
}

// The registry is written from every request handler and read by scrapes, so a
// concurrent scrape must not race with recording.
func Test_WriteIsSafeUnderConcurrency(t *testing.T) {
	registry := New()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range 100 {
			registry.Inc("requests_total", telemetry.Labels{"path": "/verify"})
			registry.Observe("duration_seconds", nil, time.Millisecond)
		}
	}()

	for range 100 {
		render(t, registry)
	}
	<-done

	assert.Contains(t, render(t, registry), "requests_total")
}
