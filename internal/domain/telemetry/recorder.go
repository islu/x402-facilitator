// Package telemetry defines the ports use cases use to report what happened,
// so they can count and time their work without knowing how metrics are stored
// or exposed.
package telemetry

import (
	"io"
	"time"
)

//go:generate go tool mockgen -destination=../../mocks/domain/telemetry.go -package=mocks -source=recorder.go

// Labels are metric dimensions, e.g. {"network": "eip155:84532"}.
type Labels map[string]string

// Recorder is the write side of metrics.
type Recorder interface {
	// Inc adds one to a counter.
	Inc(name string, labels Labels)
	// SetGauge overwrites a gauge value.
	SetGauge(name string, labels Labels, value float64)
	// Observe records a duration in a latency histogram.
	Observe(name string, labels Labels, d time.Duration)
	// Describe attaches a human-readable description to a metric name.
	Describe(name, help string)
}

// Exporter is the read side: it renders the collected metrics for a scrape.
// Kept separate from Recorder so the HTTP layer can expose metrics without
// gaining the ability to write them.
type Exporter interface {
	Write(w io.Writer) error
}
