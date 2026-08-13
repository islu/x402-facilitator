package app

import (
	"github.com/google/wire"

	"github.com/islu/x402-facilitator/internal/app/payment"
	"github.com/islu/x402-facilitator/internal/app/wallet"
)

// wire injector
var ProviderAppSet = wire.NewSet(
	payment.NewService,
	wallet.NewService,

	// Use-case configuration derived from the process config, so the services
	// depend on their own settings rather than the whole Config.
	ProvidePaymentPolicy,
	ProvideWalletConfig,
)
