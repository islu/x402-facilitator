package payment

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CheckSettleAmount(t *testing.T) {
	tests := []struct {
		name       string
		limit      *big.Int
		amount     *big.Int
		wantReject bool
	}{
		{
			name:       "below the ceiling",
			limit:      big.NewInt(1_000_000),
			amount:     big.NewInt(999_999),
			wantReject: false,
		},
		{
			// Exactly at the limit is allowed: the limit is the largest amount
			// the operator authorized, not the first one they rejected.
			name:       "exactly at the ceiling",
			limit:      big.NewInt(1_000_000),
			amount:     big.NewInt(1_000_000),
			wantReject: false,
		},
		{
			name:       "above the ceiling",
			limit:      big.NewInt(1_000_000),
			amount:     big.NewInt(1_000_001),
			wantReject: true,
		},
		{
			name:       "no ceiling configured",
			limit:      nil,
			amount:     big.NewInt(999_999_999_999),
			wantReject: false,
		},
		{
			// The mechanism owns amount validation; failing closed here would
			// reject schemes whose amount this service cannot read.
			name:       "unreadable amount passes",
			limit:      big.NewInt(1_000_000),
			amount:     nil,
			wantReject: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{MaxSettleAmount: tc.limit}

			err := policy.CheckSettleAmount(Metadata{Amount: tc.amount, Network: "eip155:84532"})

			if !tc.wantReject {
				assert.NoError(t, err)
				return
			}

			var failed *SettlementError
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, ReasonAmountTooBig, failed.Reason)
			assert.Equal(t, "eip155:84532", failed.Network)
			assert.Empty(t, failed.Transaction, "nothing was broadcast")
		})
	}
}
