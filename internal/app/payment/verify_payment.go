package payment

import (
	"context"
	"errors"
	"time"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

// VerifyPayment checks that a payment authorizes what the requirements ask for,
// without moving any funds.
//
// A payment that is invalid comes back as *payment.VerificationError; any other
// error means verification could not be completed, which callers must treat
// differently — the first rejects the buyer, the second is worth retrying.
func (s *Service) VerifyPayment(ctx context.Context, req payment.Request) (payment.VerifyResult, error) {
	meta := payment.ParseMetadata(req)

	if s.rateLimited("verify", meta) {
		s.logger.WarnContext(ctx, "verify rate limited", "payer", meta.Payer, "network", meta.Network)
		return payment.VerifyResult{}, payment.NewVerificationError(
			payment.ReasonRateLimited, "too many requests for this payer", meta.Payer)
	}

	// Policy runs at verify too, so a resource server learns a payment will be
	// refused before it serves the content, not after.
	if reason, message := s.policy.Check(meta); reason != "" {
		err := payment.NewVerificationError(reason, message, meta.Payer)
		s.recordVerifyFailure(ctx, meta, err)
		return payment.VerifyResult{}, err
	}

	s.logger.DebugContext(ctx, "verify started",
		"network", meta.Network, "scheme", meta.Scheme, "payer", meta.Payer)

	start := time.Now()
	result, err := s.processor.Verify(ctx, req)
	s.metrics.Observe(metricVerifyDuration, nil, time.Since(start))

	if err != nil {
		s.recordVerifyFailure(ctx, meta, err)
		return payment.VerifyResult{}, err
	}

	s.metrics.Inc(metricVerifications, telemetry.Labels{
		"network": meta.Network,
		"scheme":  meta.Scheme,
		"valid":   "true",
	})
	s.recordPayer(meta)
	s.logger.InfoContext(ctx, "payment verified",
		"network", meta.Network, "scheme", meta.Scheme, "payer", result.Payer)

	return result, nil
}

func (s *Service) recordVerifyFailure(ctx context.Context, meta payment.Metadata, err error) {
	var invalid *payment.VerificationError
	if !errors.As(err, &invalid) {
		// Not a verdict on the payment — an outage, a timeout, a bug. Counting
		// it as "invalid" would make a broken dependency look like a wave of
		// bad payments.
		s.logger.ErrorContext(ctx, "verification could not be completed",
			"network", meta.Network, "scheme", meta.Scheme, "payer", meta.Payer, "error", err)
		return
	}

	s.metrics.Inc(metricVerifications, telemetry.Labels{
		"network": meta.Network,
		"scheme":  meta.Scheme,
		"valid":   "false",
	})
	s.metrics.Inc(metricVerifyFailures, telemetry.Labels{
		"network": meta.Network,
		"reason":  reasonLabel(invalid.Reason),
	})
	s.logger.WarnContext(ctx, "payment rejected",
		"network", meta.Network,
		"scheme", meta.Scheme,
		"payer", cmpOr(invalid.Payer, meta.Payer),
		"reason", invalid.Reason,
		"message", invalid.Message,
	)
}

func cmpOr(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}
