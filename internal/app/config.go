// Package app holds the facilitator's use cases, one package per area, plus
// the wire set that provides them.
package app

import (
	"math/big"

	appwallet "github.com/islu/x402-facilitator/internal/app/wallet"
	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// ProvidePaymentPolicy narrows the process config down to the limits the
// payment use cases enforce.
//
// Each allowed asset is also keyed under its network's V1 alias: v1 payments
// name the network "base-sepolia", not "eip155:84532", and must meet the same
// policy.
func ProvidePaymentPolicy(cfg *config.Config) payment.Policy {
	aliases := map[string]string{}
	for _, n := range cfg.EVMNetworks {
		aliases[n.Network] = n.V1Alias
	}
	for _, n := range cfg.SVMNetworks {
		aliases[n.Network] = n.V1Alias
	}

	policy := payment.Policy{Assets: map[string]payment.AssetLimit{}}
	for _, a := range cfg.AllowedAssets {
		limit := payment.AssetLimit{Min: a.Min, Max: a.Max}
		policy.Assets[payment.AssetKey(a.Network, a.Asset)] = limit
		if alias := aliases[a.Network]; alias != "" {
			policy.Assets[payment.AssetKey(alias, a.Asset)] = limit
		}
	}
	return policy
}

// ProvideWalletConfig narrows the process config down to the wallet thresholds,
// in each network's own atomic unit.
func ProvideWalletConfig(cfg *config.Config) appwallet.Config {
	thresholds := map[string]*big.Int{}
	for _, n := range cfg.EVMNetworks {
		thresholds[n.Network] = cfg.MinBalanceWei
	}
	for _, n := range cfg.SVMNetworks {
		thresholds[n.Network] = cfg.MinBalanceLamports
	}
	return appwallet.Config{MinBalance: thresholds}
}
