// Package wallet contains the use cases that watch the facilitator's
// settlement wallets. A facilitator out of gas keeps verifying happily while
// every settlement fails, so the balance is worth surfacing on its own.
package wallet

import (
	"log/slog"
	"math/big"

	"github.com/islu/x402-facilitator/internal/domain/telemetry"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

const (
	metricBalance      = "facilitator_wallet_balance"
	metricBalanceError = "facilitator_wallet_balance_errors_total"
)

// Config holds the thresholds the wallet use cases work against.
type Config struct {
	// MinBalance is the threshold below which a network's wallet is reported
	// as low, in that chain's atomic unit (wei, lamports). A network without
	// one is never warned about.
	MinBalance map[string]*big.Int
}

type Service struct {
	wallets wallet.Registry
	metrics telemetry.Recorder
	cfg     Config
	logger  *slog.Logger
}

func NewService(
	wallets wallet.Registry,
	metrics telemetry.Recorder,
	cfg Config,
	logger *slog.Logger,
) *Service {
	metrics.Describe(metricBalance, "Settlement wallet native balance, in whole units (ETH/SOL).")
	metrics.Describe(metricBalanceError, "Failed balance reads by network.")
	return &Service{
		wallets: wallets,
		metrics: metrics,
		cfg:     cfg,
		logger:  logger.With("service", "wallet"),
	}
}
