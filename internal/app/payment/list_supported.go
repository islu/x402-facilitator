package payment

import (
	"context"

	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// ListSupported returns the payment kinds this facilitator can process.
// Resource servers read it to decide which payment options to advertise.
func (s *Service) ListSupported(ctx context.Context) payment.Supported {
	return s.processor.Supported(ctx)
}

// Ready reports whether the facilitator can serve payments at all, and why not
// when it cannot. A facilitator with no registered kinds accepts nothing, so it
// should be drained rather than sent traffic.
func (s *Service) Ready(ctx context.Context) (bool, string) {
	if len(s.processor.Supported(ctx).Kinds) == 0 {
		return false, "no payment kinds registered"
	}
	return true, ""
}
