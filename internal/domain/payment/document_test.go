package payment

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ParseMetadata(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		requirements string
		want         Metadata
	}{
		{
			name: "v2 EVM exact payment",
			payload: `{
				"x402Version": 2,
				"accepted": {"scheme": "exact", "network": "eip155:84532", "amount": "1000"},
				"payload": {"signature": "0xsig", "authorization": {"from": "0xAbC"}}
			}`,
			requirements: `{"scheme": "exact", "network": "eip155:84532", "amount": "1000"}`,
			want: Metadata{
				Version: 2,
				Scheme:  "exact",
				Network: "eip155:84532",
				Amount:  big.NewInt(1000),
				Payer:   "0xabc",
			},
		},
		{
			// v1 keeps scheme and network at the top level and calls the amount
			// maxAmountRequired.
			name: "v1 payment",
			payload: `{
				"x402Version": 1,
				"scheme": "exact",
				"network": "base-sepolia",
				"payload": {"authorization": {"from": "0xDEF"}}
			}`,
			requirements: `{"scheme": "exact", "network": "base-sepolia", "maxAmountRequired": "2500"}`,
			want: Metadata{
				Version: 1,
				Scheme:  "exact",
				Network: "base-sepolia",
				Amount:  big.NewInt(2500),
				Payer:   "0xdef",
			},
		},
		{
			name: "Permit2 authorization",
			payload: `{
				"x402Version": 2,
				"payload": {"permit2Authorization": {"from": "0x123"}}
			}`,
			requirements: `{"scheme": "upto", "network": "eip155:8453", "amount": "50"}`,
			want: Metadata{
				Version: 2,
				Scheme:  "upto",
				Network: "eip155:8453",
				Amount:  big.NewInt(50),
				Payer:   "0x123",
			},
		},
		{
			// Solana payloads carry an opaque transaction: no payer is readable
			// without chain access, and that must not become a parse failure.
			name:         "SVM payment has no readable payer",
			payload:      `{"x402Version": 2, "payload": {"transaction": "AQAAA"}}`,
			requirements: `{"scheme": "exact", "network": "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1", "amount": "10"}`,
			want: Metadata{
				Version: 2,
				Scheme:  "exact",
				Network: "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1",
				Amount:  big.NewInt(10),
			},
		},
		{
			name:         "malformed JSON yields empty metadata rather than failing",
			payload:      `{"x402Version":`,
			requirements: `not json`,
			want:         Metadata{},
		},
		{
			name:         "an unparseable amount is left unset",
			payload:      `{"x402Version": 2, "payload": {"authorization": {"from": "0xabc"}}}`,
			requirements: `{"scheme": "exact", "network": "eip155:1", "amount": "1e18"}`,
			want: Metadata{
				Version: 2,
				Scheme:  "exact",
				Network: "eip155:1",
				Payer:   "0xabc",
			},
		},
		{
			name:         "a wrongly typed payer field is ignored",
			payload:      `{"x402Version": 2, "payload": {"authorization": {"from": 42}}}`,
			requirements: `{"scheme": "exact", "network": "eip155:1", "amount": "1"}`,
			want: Metadata{
				Version: 2,
				Scheme:  "exact",
				Network: "eip155:1",
				Amount:  big.NewInt(1),
			},
		},
		{
			name:         "the inner payload object is accepted directly",
			payload:      `{"authorization": {"from": "0xabc"}}`,
			requirements: `{"scheme": "exact", "network": "eip155:1", "amount": "1"}`,
			want: Metadata{
				Scheme:  "exact",
				Network: "eip155:1",
				Amount:  big.NewInt(1),
				Payer:   "0xabc",
			},
		},
		{
			// Checksummed and lowercase spellings of one address must resolve to
			// the same payer, or a rate limit is bypassed by re-casing the hex.
			name:         "a checksummed hex payer is lowercased",
			payload:      `{"payload":{"authorization":{"from":"0xAbCdEf0123456789012345678901234567890123"}}}`,
			requirements: `{}`,
			want:         Metadata{Payer: "0xabcdef0123456789012345678901234567890123"},
		},
		{
			// Base58 Solana keys are case-sensitive, so normalization must only
			// touch hex.
			name:         "a base58 payer is left untouched",
			payload:      `{"payload":{"payer":"EtWTRABZaYq6iMfeYKouRu166VU2xqa1"}}`,
			requirements: `{}`,
			want:         Metadata{Payer: "EtWTRABZaYq6iMfeYKouRu166VU2xqa1"},
		},
		{
			name:         "an empty request yields empty metadata",
			payload:      ``,
			requirements: ``,
			want:         Metadata{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMetadata(Request{
				Payload:      []byte(tc.payload),
				Requirements: []byte(tc.requirements),
			})

			assert.Equal(t, tc.want.Version, got.Version, "version")
			assert.Equal(t, tc.want.Scheme, got.Scheme, "scheme")
			assert.Equal(t, tc.want.Network, got.Network, "network")
			assert.Equal(t, tc.want.Payer, got.Payer, "payer")
			if tc.want.Amount == nil {
				assert.Nil(t, got.Amount, "amount")
				return
			}
			require.NotNil(t, got.Amount, "amount")
			assert.Zero(t, tc.want.Amount.Cmp(got.Amount), "amount: want %s, got %s", tc.want.Amount, got.Amount)
		})
	}
}
