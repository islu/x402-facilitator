package evm

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	evmmech "github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

// eip3009Types is the TransferWithAuthorization payload every EVM exact payment
// is signed over.
var eip3009Types = map[string][]evmmech.TypedDataField{
	"TransferWithAuthorization": {
		{Name: "from", Type: "address"},
		{Name: "to", Type: "address"},
		{Name: "value", Type: "uint256"},
		{Name: "validAfter", Type: "uint256"},
		{Name: "validBefore", Type: "uint256"},
		{Name: "nonce", Type: "bytes32"},
	},
}

func usdcDomain() evmmech.TypedDataDomain {
	return evmmech.TypedDataDomain{
		Name:              "USDC",
		Version:           "2",
		ChainID:           big.NewInt(84532),
		VerifyingContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
	}
}

// permit2Domain has no version field, which is the case that breaks a naive
// EIP-712 domain encoder.
func permit2Domain() evmmech.TypedDataDomain {
	return evmmech.TypedDataDomain{
		Name:              "Permit2",
		ChainID:           big.NewInt(84532),
		VerifyingContract: "0x000000000022D473030F116dDEE9F6B43aC78BA3",
	}
}

func eip3009Message(from string) map[string]any {
	return map[string]any{
		"from":        from,
		"to":          "0x1111111111111111111111111111111111111111",
		"value":       "1000",
		"validAfter":  "0",
		"validBefore": "9999999999",
		"nonce":       "0x0000000000000000000000000000000000000000000000000000000000000001",
	}
}

// typedDataDigest builds the EIP-712 digest a payer's wallet signs. It is
// written independently of VerifyTypedData, so the test would catch a change in
// how the production path hashes a payment.
func typedDataDigest(
	t *testing.T,
	domain evmmech.TypedDataDomain,
	fieldTypes map[string][]evmmech.TypedDataField,
	primaryType string,
	message map[string]any,
) []byte {
	t.Helper()

	domainFields := []apitypes.Type{{Name: "name", Type: "string"}}
	if domain.Version != "" {
		domainFields = append(domainFields, apitypes.Type{Name: "version", Type: "string"})
	}
	domainFields = append(domainFields,
		apitypes.Type{Name: "chainId", Type: "uint256"},
		apitypes.Type{Name: "verifyingContract", Type: "address"},
	)

	typed := apitypes.TypedData{
		Types:       apitypes.Types{"EIP712Domain": domainFields},
		PrimaryType: primaryType,
		Domain: apitypes.TypedDataDomain{
			Name:              domain.Name,
			Version:           domain.Version,
			ChainId:           (*ethmath.HexOrDecimal256)(domain.ChainID),
			VerifyingContract: domain.VerifyingContract,
		},
		Message: message,
	}
	for name, fields := range fieldTypes {
		converted := make([]apitypes.Type, len(fields))
		for i, field := range fields {
			converted[i] = apitypes.Type{Name: field.Name, Type: field.Type}
		}
		typed.Types[name] = converted
	}

	structHash, err := typed.HashStruct(primaryType, typed.Message)
	require.NoError(t, err, "hash message")
	domainSeparator, err := typed.HashStruct("EIP712Domain", typed.Domain.Map())
	require.NoError(t, err, "hash domain")

	preimage := append([]byte{0x19, 0x01}, domainSeparator...)
	preimage = append(preimage, structHash...)
	return crypto.Keccak256(preimage)
}

func Test_VerifyTypedData(t *testing.T) {
	payerKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	otherKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	payer := crypto.PubkeyToAddress(payerKey.PublicKey).Hex()
	simpleTypes := map[string][]evmmech.TypedDataField{
		"Simple": {{Name: "from", Type: "address"}},
	}

	tests := []struct {
		name        string
		domain      evmmech.TypedDataDomain
		fieldTypes  map[string][]evmmech.TypedDataField
		primaryType string
		// signed is what the wallet put its signature over; verified is what the
		// facilitator checks. They differ only in the tampering cases.
		signed      map[string]any
		verified    map[string]any
		signWith    *ecdsa.PrivateKey
		claimed     string
		legacyVFlag bool
		signature   []byte
		wantValid   bool
		wantErr     bool
	}{
		{
			name:        "a valid EIP-3009 signature is accepted",
			domain:      usdcDomain(),
			fieldTypes:  eip3009Types,
			primaryType: "TransferWithAuthorization",
			signed:      eip3009Message(payer),
			signWith:    payerKey,
			claimed:     payer,
			wantValid:   true,
		},
		{
			// Wallets return v as 27/28 while go-ethereum signs with 0/1; both
			// encodings have to verify or every wallet payment is rejected.
			name:        "a signature with the wallet v encoding is accepted",
			domain:      usdcDomain(),
			fieldTypes:  eip3009Types,
			primaryType: "TransferWithAuthorization",
			signed:      eip3009Message(payer),
			signWith:    payerKey,
			claimed:     payer,
			legacyVFlag: true,
			wantValid:   true,
		},
		{
			name:        "a signature from another key is rejected",
			domain:      usdcDomain(),
			fieldTypes:  eip3009Types,
			primaryType: "TransferWithAuthorization",
			signed:      eip3009Message(payer),
			signWith:    otherKey,
			claimed:     payer,
			wantValid:   false,
		},
		{
			// This is what stops a payer being charged more than they authorized.
			name:        "a tampered amount is rejected",
			domain:      usdcDomain(),
			fieldTypes:  eip3009Types,
			primaryType: "TransferWithAuthorization",
			signed:      eip3009Message(payer),
			verified: func() map[string]any {
				m := eip3009Message(payer)
				m["value"] = "100000"
				return m
			}(),
			signWith:  payerKey,
			claimed:   payer,
			wantValid: false,
		},
		{
			// Permit2's domain declares no version. Naming a dropped domain
			// field makes hashing fail outright, rejecting every Permit2 payment.
			name:        "a version-less domain verifies",
			domain:      permit2Domain(),
			fieldTypes:  simpleTypes,
			primaryType: "Simple",
			signed:      map[string]any{"from": payer},
			signWith:    payerKey,
			claimed:     payer,
			wantValid:   true,
		},
		{
			name:        "a signature of the wrong length is an error, not a verdict",
			domain:      usdcDomain(),
			fieldTypes:  eip3009Types,
			primaryType: "TransferWithAuthorization",
			signed:      eip3009Message(payer),
			claimed:     payer,
			signature:   []byte{0x01, 0x02},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signature := tt.signature
			if signature == nil {
				digest := typedDataDigest(t, tt.domain, tt.fieldTypes, tt.primaryType, tt.signed)
				signature, err = crypto.Sign(digest, tt.signWith)
				require.NoError(t, err)
				if tt.legacyVFlag {
					signature[64] += 27
				}
			}

			verified := tt.verified
			if verified == nil {
				verified = tt.signed
			}

			signer := &Signer{}
			valid, err := signer.VerifyTypedData(context.Background(), tt.claimed, tt.domain,
				tt.fieldTypes, tt.primaryType, verified, signature)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValid, valid)
		})
	}
}
