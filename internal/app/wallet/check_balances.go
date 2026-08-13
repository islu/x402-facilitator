package wallet

import (
	"context"
	"sort"

	"github.com/islu/x402-facilitator/internal/domain/telemetry"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

// CheckBalances reads every settlement wallet, exports its balance as a gauge
// and warns about the ones running low. It returns the balances it managed to
// read.
//
// One unreachable chain must not hide the others, so a failed read is counted
// and logged while the sweep continues.
func (s *Service) CheckBalances(ctx context.Context) []wallet.Balance {
	balances := make([]wallet.Balance, 0, len(s.wallets))

	for _, network := range sortedNetworks(s.wallets) {
		balance, err := s.wallets[network].Balance(ctx)
		if err != nil {
			s.logger.WarnContext(ctx, "failed to read settlement wallet balance",
				"network", network, "error", err)
			s.metrics.Inc(metricBalanceError, telemetry.Labels{"network": network})
			continue
		}

		s.metrics.SetGauge(metricBalance, telemetry.Labels{"network": network}, balance.Whole())

		if balance.Below(s.cfg.MinBalance) {
			s.logger.WarnContext(ctx, "settlement wallet balance is low",
				"network", network,
				"address", balance.Address,
				"balance", balance.Whole(),
				"threshold_atomic", s.cfg.MinBalance.String(),
			)
		} else {
			s.logger.DebugContext(ctx, "settlement wallet balance",
				"network", network, "address", balance.Address, "balance", balance.Whole())
		}

		balances = append(balances, balance)
	}

	return balances
}

// sortedNetworks keeps log and scrape order stable across runs.
func sortedNetworks(registry wallet.Registry) []string {
	networks := make([]string, 0, len(registry))
	for network := range registry {
		networks = append(networks, network)
	}
	sort.Strings(networks)
	return networks
}
