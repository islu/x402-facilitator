// Package payment contains the facilitator's payment use cases: verifying a
// payment, settling it, and reporting what it can process.
//
// The policy that used to live in x402 lifecycle hooks lives here instead —
// rate limiting and the settlement ceiling are decisions this service makes
// before handing the payment to the mechanism, so they are testable without a
// chain and visible in one place.
package payment

import (
	"log/slog"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/ratelimit"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

// Metric names. Kept stable: dashboards and alerts are built on them.
const (
	metricVerifications  = "facilitator_verifications_total"
	metricVerifyFailures = "facilitator_verify_failures_total"
	metricSettlements    = "facilitator_settlements_total"
	metricSettleFailures = "facilitator_settle_failures_total"
	metricRejected       = "facilitator_settle_rejected_total"
	metricRateLimited    = "facilitator_rate_limited_total"
	metricVerifyDuration = "facilitator_verify_duration_seconds"
	metricSettleDuration = "facilitator_settle_duration_seconds"
)

type Service struct {
	processor   payment.Processor
	limiter     ratelimit.Limiter
	metrics     telemetry.Recorder
	policy      payment.Policy
	logger      *slog.Logger
	settlements *settlementLog
}

func NewService(
	processor payment.Processor,
	limiter ratelimit.Limiter,
	metrics telemetry.Recorder,
	policy payment.Policy,
	logger *slog.Logger,
) *Service {
	describeMetrics(metrics)
	return &Service{
		processor:   processor,
		limiter:     limiter,
		metrics:     metrics,
		policy:      policy,
		logger:      logger.With("service", "payment"),
		settlements: newSettlementLog(settlementRetention),
	}
}

// rateLimited reports whether the payer has used up its quota.
//
// The quota counts only payments that proved the payer signed them (see
// recordPayer), so a forged payload naming someone else's address cannot
// exhaust that payer's quota. Unproven traffic is bounded per caller IP at the
// HTTP layer instead.
//
// A payer the payload does not identify (Solana carries an opaque transaction)
// is not limited: bucketing every anonymous request together would let one
// caller exhaust the quota for all of them.
func (s *Service) rateLimited(operation string, meta payment.Metadata) bool {
	if !s.limiter.Enabled() || meta.Payer == "" || !s.limiter.Exceeded(meta.Payer) {
		return false
	}
	s.metrics.Inc(metricRateLimited, telemetry.Labels{"operation": operation})
	return true
}

// recordPayer counts a payment against its payer's quota once the mechanism
// has accepted it.
func (s *Service) recordPayer(meta payment.Metadata) {
	if s.limiter.Enabled() && meta.Payer != "" {
		s.limiter.Allow(meta.Payer)
	}
}

// reasonLabel keeps unknown failure reasons out of the metric label space,
// which would otherwise grow without bound.
func reasonLabel(reason string) string {
	if reason == "" {
		return "unknown"
	}
	return reason
}

func describeMetrics(metrics telemetry.Recorder) {
	metrics.Describe(metricVerifications, "Payment verifications by network, scheme and validity.")
	metrics.Describe(metricVerifyFailures, "Verification failures by network and reason.")
	metrics.Describe(metricSettlements, "Settlements by network, scheme and outcome.")
	metrics.Describe(metricSettleFailures, "Settlement failures by network and reason.")
	metrics.Describe(metricRejected, "Settlements rejected by facilitator policy before reaching the chain.")
	metrics.Describe(metricRateLimited, "Requests rejected by the per-payer rate limiter.")
	metrics.Describe(metricVerifyDuration, "Verification latency in seconds.")
	metrics.Describe(metricSettleDuration, "Settlement latency in seconds, including confirmation.")
}
