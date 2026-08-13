// Package telemetry implements the domain telemetry ports with a
// dependency-free metric registry that exposes Prometheus text format.
//
// It covers the counters, gauges and latency histograms a facilitator needs
// without pulling in a client library; swap it for prometheus/client_golang if
// you need exemplars, summaries or a pushgateway — the domain ports stay put.
package telemetry

import (
	"fmt"
	"io"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

// defaultBuckets are upper bounds in seconds. Settlement waits on block
// inclusion, so the tail extends well past a typical HTTP histogram.
var defaultBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type histogram struct {
	counts []uint64
	sum    float64
	total  uint64
}

// series is one metric name plus one label set. HELP text is not stored here:
// it belongs to the metric name, so the registry keeps it in a separate map
// shared by every series of that name.
type series struct {
	name   string
	kind   string
	labels telemetry.Labels
	value  float64
	hist   *histogram
}

// Registry collects metrics in memory and renders them in Prometheus text
// format, implementing both telemetry.Recorder and telemetry.Exporter. All
// methods are safe for concurrent use.
type Registry struct {
	mu     sync.Mutex
	series map[string]*series
	help   map[string]string
}

var (
	_ telemetry.Recorder = (*Registry)(nil)
	_ telemetry.Exporter = (*Registry)(nil)
)

// New creates an empty registry.
func New() *Registry {
	return &Registry{
		series: map[string]*series{},
		help:   map[string]string{},
	}
}

// Describe attaches a HELP string to a metric name.
func (r *Registry) Describe(name, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.help[name] = help
}

// Inc adds 1 to a counter.
func (r *Registry) Inc(name string, labels telemetry.Labels) {
	r.Add(name, labels, 1)
}

// Add adds delta to a counter.
func (r *Registry) Add(name string, labels telemetry.Labels, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.seriesFor(name, "counter", labels)
	s.value += delta
}

// SetGauge overwrites a gauge value.
func (r *Registry) SetGauge(name string, labels telemetry.Labels, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.seriesFor(name, "gauge", labels)
	s.value = value
}

// Observe records a duration in a latency histogram.
func (r *Registry) Observe(name string, labels telemetry.Labels, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.seriesFor(name, "histogram", labels)
	if s.hist == nil {
		s.hist = &histogram{counts: make([]uint64, len(defaultBuckets))}
	}
	seconds := d.Seconds()
	for i, bound := range defaultBuckets {
		if seconds <= bound {
			s.hist.counts[i]++
		}
	}
	s.hist.sum += seconds
	s.hist.total++
}

// seriesFor returns the series for a name/label pair, creating it if needed.
// Callers must hold r.mu.
func (r *Registry) seriesFor(name, kind string, labels telemetry.Labels) *series {
	key := seriesKey(name, labels)
	s, ok := r.series[key]
	if !ok {
		s = &series{name: name, kind: kind, labels: labels}
		r.series[key] = s
	}
	return s
}

// Write renders every metric in Prometheus text exposition format.
func (r *Registry) Write(w io.Writer) error {
	r.mu.Lock()
	snapshot := make([]*series, 0, len(r.series))
	for _, s := range r.series {
		clone := *s
		if s.hist != nil {
			hist := *s.hist
			hist.counts = append([]uint64(nil), s.hist.counts...)
			clone.hist = &hist
		}
		snapshot = append(snapshot, &clone)
	}
	help := make(map[string]string, len(r.help))
	maps.Copy(help, r.help)
	r.mu.Unlock()

	sort.Slice(snapshot, func(i, j int) bool {
		if snapshot[i].name != snapshot[j].name {
			return snapshot[i].name < snapshot[j].name
		}
		return seriesKey(snapshot[i].name, snapshot[i].labels) < seriesKey(snapshot[j].name, snapshot[j].labels)
	})

	var buf strings.Builder
	written := map[string]bool{}
	for _, s := range snapshot {
		if !written[s.name] {
			written[s.name] = true
			if h := help[s.name]; h != "" {
				fmt.Fprintf(&buf, "# HELP %s %s\n", s.name, h)
			}
			fmt.Fprintf(&buf, "# TYPE %s %s\n", s.name, s.kind)
		}
		if s.kind == "histogram" && s.hist != nil {
			for i, bound := range defaultBuckets {
				fmt.Fprintf(&buf, "%s_bucket%s %d\n",
					s.name, renderLabels(s.labels, "le", formatFloat(bound)), s.hist.counts[i])
			}
			fmt.Fprintf(&buf, "%s_bucket%s %d\n", s.name, renderLabels(s.labels, "le", "+Inf"), s.hist.total)
			fmt.Fprintf(&buf, "%s_sum%s %s\n", s.name, renderLabels(s.labels), formatFloat(s.hist.sum))
			fmt.Fprintf(&buf, "%s_count%s %d\n", s.name, renderLabels(s.labels), s.hist.total)
			continue
		}
		fmt.Fprintf(&buf, "%s%s %s\n", s.name, renderLabels(s.labels), formatFloat(s.value))
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func seriesKey(name string, labels telemetry.Labels) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(labels[k])
	}
	return b.String()
}

// renderLabels formats a label set, optionally appending one extra pair
// (used for the histogram "le" bucket bound).
func renderLabels(labels telemetry.Labels, extra ...string) string {
	pairs := make([]string, 0, len(labels)+1)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// %q escapes quotes, backslashes and control characters, which covers
		// what the exposition format requires.
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	if len(extra) == 2 {
		pairs = append(pairs, fmt.Sprintf("%s=%q", extra[0], extra[1]))
	}
	if len(pairs) == 0 {
		return ""
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
