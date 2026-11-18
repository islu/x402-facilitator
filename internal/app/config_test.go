package app

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// A v1 payment names its network by the legacy alias; the allowlist must still
// apply to it, or v1 becomes a way around the policy.
func Test_ProvidePaymentPolicy_CoversV1Aliases(t *testing.T) {
	cfg := &config.Config{
		EVMNetworks:   []config.EVMNetwork{{Network: "eip155:84532", V1Alias: "base-sepolia"}},
		AllowedAssets: []config.AllowedAsset{{Network: "eip155:84532", Asset: "0xAAA", Max: big.NewInt(10)}},
	}

	policy := ProvidePaymentPolicy(cfg)

	for _, network := range []string{"eip155:84532", "base-sepolia"} {
		reason, _ := policy.Check(payment.Metadata{Network: network, Asset: "0xaaa", Amount: big.NewInt(11)})
		assert.Equal(t, payment.ReasonAmountTooBig, reason, network)
	}
}
