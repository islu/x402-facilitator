package payment

import (
	"context"
	"errors"
	"time"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

// SettlePayment broadcasts a payment and waits for it to be confirmed.
//
// A payment is settled at most once at a time: a retry that arrives while the
// first attempt is in flight waits for it, and a retry after a transaction was
// broadcast gets the recorded outcome instead of a second transaction.
func (s *Service) SettlePayment(ctx context.Context, req payment.Request) (payment.SettleResult, error) {
	key := settlementKey(req)
	entry, leader := s.settlements.begin(key)
	if !leader {
		s.logger.InfoContext(ctx, "duplicate settlement joined the recorded attempt")
		return entry.wait(ctx)
	}

	result, err := s.settle(ctx, req)
	s.settlements.finish(key, entry, result, err)
	return result, err
}

// settle runs local policy, then the mechanism.
//
// Local policy runs first — the rate limit and the asset allowlist — because
// once the transaction is broadcast the facilitator has spent gas and the payer
// may have been charged, whatever happens next.
func (s *Service) settle(ctx context.Context, req payment.Request) (payment.SettleResult, error) {
	meta := payment.ParseMetadata(req)

	if s.rateLimited("settle", meta) {
		s.logger.WarnContext(ctx, "settle rate limited", "payer", meta.Payer, "network", meta.Network)
		return payment.SettleResult{}, payment.NewSettlementError(
			payment.ReasonRateLimited, "too many requests for this payer", meta.Payer, meta.Network, "")
	}

	if reason, message := s.policy.Check(meta); reason != "" {
		s.metrics.Inc(metricRejected, telemetry.Labels{
			"network": meta.Network,
			"reason":  reason,
		})
		s.logger.WarnContext(ctx, "settlement refused by policy",
			"payer", meta.Payer, "network", meta.Network, "asset", meta.Asset, "amount", meta.Amount, "reason", reason)
		return payment.SettleResult{}, payment.NewSettlementError(reason, message, meta.Payer, meta.Network, "")
	}

	s.logger.InfoContext(ctx, "settlement started",
		"network", meta.Network, "scheme", meta.Scheme, "payer", meta.Payer, "amount", meta.Amount)

	start := time.Now()
	result, err := s.processor.Settle(ctx, req)
	s.metrics.Observe(metricSettleDuration, nil, time.Since(start))

	if err != nil {
		s.recordSettleFailure(ctx, meta, err)
		return payment.SettleResult{}, err
	}

	s.metrics.Inc(metricSettlements, telemetry.Labels{
		"network": result.Network,
		"scheme":  meta.Scheme,
		"success": "true",
	})
	s.recordPayer(meta)
	// The only point where the transaction hash and the payer are both known —
	// extend here to record settlements in a durable store.
	s.logger.InfoContext(ctx, "payment settled",
		"network", result.Network,
		"scheme", meta.Scheme,
		"transaction", result.Transaction,
		"payer", result.Payer,
	)

	return result, nil
}

func (s *Service) recordSettleFailure(ctx context.Context, meta payment.Metadata, err error) {
	var failed *payment.SettlementError
	if !errors.As(err, &failed) {
		s.logger.ErrorContext(ctx, "settlement could not be completed",
			"network", meta.Network, "scheme", meta.Scheme, "payer", meta.Payer, "error", err)
		return
	}

	s.metrics.Inc(metricSettlements, telemetry.Labels{
		"network": cmpOr(failed.Network, meta.Network),
		"scheme":  meta.Scheme,
		"success": "false",
	})
	s.metrics.Inc(metricSettleFailures, telemetry.Labels{
		"network": cmpOr(failed.Network, meta.Network),
		"reason":  reasonLabel(failed.Reason),
	})
	// Retrying here would re-broadcast a payment that may still land, so the
	// failure is reported rather than recovered.
	s.logger.ErrorContext(ctx, "settlement failed",
		"network", cmpOr(failed.Network, meta.Network),
		"scheme", meta.Scheme,
		"payer", cmpOr(failed.Payer, meta.Payer),
		"reason", failed.Reason,
		"message", failed.Message,
		"transaction", failed.Transaction,
	)
}
