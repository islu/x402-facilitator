// Package app holds the facilitator's use cases, one package per area, plus
// the wire set that provides them.
package app

import (
	appwallet "github.com/islu/x402-facilitator/internal/app/wallet"
	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// ProvidePaymentPolicy narrows the process config down to the limits the
// payment use cases enforce.
func ProvidePaymentPolicy(cfg *config.Config) payment.Policy {
	return payment.Policy{MaxSettleAmount: cfg.MaxSettleAmount}
}

// ProvideWalletConfig narrows the process config down to the wallet thresholds.
func ProvideWalletConfig(cfg *config.Config) appwallet.Config {
	return appwallet.Config{MinBalance: cfg.MinBalanceWei}
}
