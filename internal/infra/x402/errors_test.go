package x402

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	x402sdk "github.com/x402-foundation/x402/go/v2"

	"github.com/islu/x402-facilitator/internal/domain/payment"
)

var errRPCDown = errors.New("rpc endpoint unreachable")

func Test_translateVerifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		// wantVerdict means the error is a verdict on the payment and must
		// arrive as *payment.VerificationError.
		wantVerdict bool
		wantReason  string
		wantMessage string
		wantPayer   string
	}{
		{
			name:        "an SDK verify error becomes a domain verification error",
			err:         x402sdk.NewVerifyError("invalid_signature", "0xabc", "bad signature"),
			wantVerdict: true,
			wantReason:  "invalid_signature",
			wantMessage: "bad signature",
			wantPayer:   "0xabc",
		},
		{
			name:        "a wrapped SDK verify error is still a verdict",
			err:         fmt.Errorf("mechanism: %w", x402sdk.NewVerifyError("insufficient_funds", "", "")),
			wantVerdict: true,
			wantReason:  "insufficient_funds",
		},
		{
			// An outage is not a verdict: it must stay a plain error so the use
			// case reports "could not verify" rather than "payment invalid".
			name:        "a system error passes through unchanged",
			err:         errRPCDown,
			wantVerdict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := translateVerifyError(tt.err)

			var invalid *payment.VerificationError
			if !tt.wantVerdict {
				require.ErrorIs(t, err, tt.err)
				assert.NotErrorAs(t, err, &invalid)
				return
			}
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, tt.wantReason, invalid.Reason)
			assert.Equal(t, tt.wantMessage, invalid.Message)
			assert.Equal(t, tt.wantPayer, invalid.Payer)
		})
	}
}

func Test_translateSettleError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantVerdict     bool
		wantReason      string
		wantNetwork     string
		wantTransaction string
	}{
		{
			// The transaction hash has to survive: it is the only handle on a
			// payment that was broadcast and then failed.
			name: "an SDK settle error keeps the broadcast transaction",
			err: x402sdk.NewSettleError(
				"transaction_failed", "0xabc", "eip155:84532", "0xdead", "reverted"),
			wantVerdict:     true,
			wantReason:      "transaction_failed",
			wantNetwork:     "eip155:84532",
			wantTransaction: "0xdead",
		},
		{
			name:        "a failure before broadcast carries no transaction",
			err:         x402sdk.NewSettleError("insufficient_funds", "0xabc", "eip155:84532", "", ""),
			wantVerdict: true,
			wantReason:  "insufficient_funds",
			wantNetwork: "eip155:84532",
		},
		{
			name:        "a system error passes through unchanged",
			err:         errRPCDown,
			wantVerdict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := translateSettleError(tt.err)

			var failed *payment.SettlementError
			if !tt.wantVerdict {
				require.ErrorIs(t, err, tt.err)
				assert.NotErrorAs(t, err, &failed)
				return
			}
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, tt.wantReason, failed.Reason)
			assert.Equal(t, tt.wantNetwork, failed.Network)
			assert.Equal(t, tt.wantTransaction, failed.Transaction)
		})
	}
}
