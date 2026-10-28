package payment

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_PolicyCheck(t *testing.T) {
	const (
		network = "eip155:84532"
		usdc    = "0x036cbd53842c5426634e7929541ec2318f3dcf7e"
	)
	limited := Policy{Assets: map[string]AssetLimit{
		AssetKey(network, usdc): {Min: big.NewInt(100), Max: big.NewInt(1_000_000)},
	}}

	tests := []struct {
		name       string
		policy     Policy
		asset      string
		amount     *big.Int
		wantReason string
	}{
		{
			name:   "within the bounds",
			policy: limited,
			asset:  usdc,
			amount: big.NewInt(999_999),
		},
		{
			// The bounds are the largest and smallest amounts the operator
			// authorized, not the first ones they rejected.
			name:   "exactly at the ceiling",
			policy: limited,
			asset:  usdc,
			amount: big.NewInt(1_000_000),
		},
		{
			name:       "above the ceiling",
			policy:     limited,
			asset:      usdc,
			amount:     big.NewInt(1_000_001),
			wantReason: ReasonAmountTooBig,
		},
		{
			name:       "below the minimum",
			policy:     limited,
			asset:      usdc,
			amount:     big.NewInt(99),
			wantReason: ReasonAmountTooSmall,
		},
		{
			// A checksummed spelling of an allowed address is the same asset.
			name:   "the asset matches case-insensitively",
			policy: limited,
			asset:  "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			amount: big.NewInt(1000),
		},
		{
			name:       "an asset outside the allowlist",
			policy:     limited,
			asset:      "0x000000000000000000000000000000000000dead",
			amount:     big.NewInt(1000),
			wantReason: ReasonAssetNotAllowed,
		},
		{
			// The allowlist only guards the gas wallet if it fails closed.
			name:       "a missing asset with an allowlist configured",
			policy:     limited,
			asset:      "",
			amount:     big.NewInt(1000),
			wantReason: ReasonAssetNotAllowed,
		},
		{
			// The mechanism owns amount validation; failing closed here would
			// reject schemes whose amount this service cannot read.
			name:   "an unreadable amount passes",
			policy: limited,
			asset:  usdc,
			amount: nil,
		},
		{
			name:   "no allowlist configured accepts anything",
			policy: Policy{},
			asset:  "0x000000000000000000000000000000000000dead",
			amount: big.NewInt(999_999_999_999),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, _ := tt.policy.Check(Metadata{
				Network: network,
				Asset:   normalizeAddress(tt.asset),
				Amount:  tt.amount,
			})

			assert.Equal(t, tt.wantReason, reason)
		})
	}
}
