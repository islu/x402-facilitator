package payment

import (
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/ratelimit"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
	mocks "github.com/islu/x402-facilitator/internal/mocks/domain"
)

// test data
const (
	testPayer   = "0xabcdef0123456789012345678901234567890123"
	testNetwork = "eip155:84532"
	testScheme  = "exact"
	testAsset   = "0x036cbd53842c5426634e7929541ec2318f3dcf7e"
)

type serviceDeps struct {
	processor *mocks.MockProcessor
	limiter   ratelimit.Limiter
	metrics   telemetry.Recorder
	policy    payment.Policy
	logger    *slog.Logger
}

func newServiceDeps(ctrl *gomock.Controller) serviceDeps {
	return serviceDeps{
		processor: mocks.NewMockProcessor(ctrl),
		limiter:   disabledLimiter{},
		metrics:   discardRecorder{},
		policy:    payment.Policy{},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newService(deps serviceDeps) *Service {
	return &Service{
		processor:   deps.processor,
		limiter:     deps.limiter,
		metrics:     deps.metrics,
		policy:      deps.policy,
		logger:      deps.logger,
		settlements: newSettlementLog(settlementRetention),
	}
}

// evmRequest builds a v2 EVM exact payment for the given payer and amount.
func evmRequest(t *testing.T, payer, amount string) payment.Request {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"x402Version": 2,
		"accepted": map[string]any{
			"scheme":  testScheme,
			"network": testNetwork,
			"amount":  amount,
			"asset":   testAsset,
		},
		"payload": map[string]any{
			"signature":     "0xsig",
			"authorization": map[string]any{"from": payer},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	requirements, err := json.Marshal(map[string]any{
		"scheme":  testScheme,
		"network": testNetwork,
		"amount":  amount,
		"asset":   testAsset,
	})
	if err != nil {
		t.Fatalf("marshal requirements: %v", err)
	}

	return payment.Request{Payload: payload, Requirements: requirements}
}

// disabledLimiter is the default: tests that care about rate limiting install
// countingLimiter instead.
type disabledLimiter struct{}

func (disabledLimiter) Allow(string) bool    { return true }
func (disabledLimiter) Exceeded(string) bool { return false }
func (disabledLimiter) Enabled() bool        { return false }

// countingLimiter allows the first n recorded calls and records the keys it saw.
type countingLimiter struct {
	remaining int
	keys      []string
}

func (l *countingLimiter) Allow(key string) bool {
	l.keys = append(l.keys, key)
	if l.remaining <= 0 {
		return false
	}
	l.remaining--
	return true
}

func (l *countingLimiter) Exceeded(string) bool { return l.remaining <= 0 }

func (l *countingLimiter) Enabled() bool { return true }

// discardRecorder drops metrics; tests that assert on them use recordingRecorder.
type discardRecorder struct{}

func (discardRecorder) Inc(string, telemetry.Labels)                    {}
func (discardRecorder) SetGauge(string, telemetry.Labels, float64)      {}
func (discardRecorder) Observe(string, telemetry.Labels, time.Duration) {}
func (discardRecorder) Describe(string, string)                         {}

// recordingRecorder captures counter increments keyed by name and labels.
type recordingRecorder struct {
	counters map[string]int
	observed []string
}

func newRecordingRecorder() *recordingRecorder {
	return &recordingRecorder{counters: map[string]int{}}
}

func (r *recordingRecorder) Inc(name string, labels telemetry.Labels) {
	r.counters[key(name, labels)]++
}

func (r *recordingRecorder) SetGauge(name string, labels telemetry.Labels, _ float64) {
	r.counters[key(name, labels)]++
}

func (r *recordingRecorder) Observe(name string, _ telemetry.Labels, _ time.Duration) {
	r.observed = append(r.observed, name)
}

func (r *recordingRecorder) Describe(string, string) {}

func (r *recordingRecorder) count(name string, labels telemetry.Labels) int {
	return r.counters[key(name, labels)]
}

func key(name string, labels telemetry.Labels) string {
	var b strings.Builder
	b.WriteString(name)
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		b.WriteString("|" + k + "=" + labels[k])
	}
	return b.String()
}
