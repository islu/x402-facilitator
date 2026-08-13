package wallet

import (
	"context"
	"time"
)

// Watch runs CheckBalances every interval until ctx is cancelled. An interval
// of zero or less disables the watch.
//
// The first check runs immediately, so a facilitator started with an empty
// wallet complains at boot rather than at the first payment.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.CheckBalances(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.CheckBalances(ctx)
		}
	}
}
