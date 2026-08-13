// Package wallet models the facilitator's settlement wallets — the accounts
// that pay gas to put payments on-chain.
package wallet

import "math/big"

// Balance is one settlement wallet's native-currency balance at a point in time.
type Balance struct {
	// Network is the CAIP-2 identifier the wallet settles on.
	Network string
	Address string
	// Atomic is the balance in the chain's smallest unit (wei, lamports).
	Atomic *big.Int
	// UnitDivisor converts Atomic to whole units (1e18 for wei, 1e9 for
	// lamports). Chains disagree on this, so the wallet reports it rather than
	// leaving callers to guess from the network id.
	UnitDivisor float64
}

// Whole returns the balance in whole units (ETH, SOL), for humans and gauges.
func (b Balance) Whole() float64 {
	if b.Atomic == nil || b.UnitDivisor == 0 {
		return 0
	}
	whole, _ := new(big.Float).Quo(new(big.Float).SetInt(b.Atomic), big.NewFloat(b.UnitDivisor)).Float64()
	return whole
}

// Below reports whether the balance is under threshold, expressed in the
// chain's atomic unit. A nil threshold is never crossed.
func (b Balance) Below(threshold *big.Int) bool {
	if threshold == nil || b.Atomic == nil {
		return false
	}
	return b.Atomic.Cmp(threshold) < 0
}
