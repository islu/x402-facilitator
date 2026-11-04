//go:build e2e

package e2e

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setup is one chain with a token, a facilitator wallet with gas, a buyer
// holding 1 token, and a seller.
type setup struct {
	chain       *chain
	token       common.Address
	facilitator common.Address
	buyer       common.Address
	payTo       common.Address
	pay         func(amount string) []byte
}

func newSetup(t *testing.T, extraEnv ...string) (*setup, *facilitator) {
	t.Helper()
	c := startChain(t)
	token := c.deployToken()
	key, facilitatorAddr := c.newAccount()
	buyerKey, buyer := newKey(t)
	_, payTo := newKey(t)
	c.mint(token, buyer, 1_000_000)

	s := &setup{
		chain:       c,
		token:       token,
		facilitator: facilitatorAddr,
		buyer:       buyer,
		payTo:       payTo,
		pay: func(amount string) []byte {
			return pay(t, buyerKey, requirements(token, payTo, amount))
		},
	}
	return s, startFacilitator(t, c, key, token, extraEnv...)
}

func TestSettle(t *testing.T) {
	s, f := newSetup(t)
	body := s.pay("10000")

	verified := f.verify(body)
	require.True(t, verified.IsValid, verified.InvalidReason)
	assert.True(t, strings.EqualFold(s.buyer.Hex(), verified.Payer))

	// Concurrent retries of one payment: one transaction, every caller gets its hash.
	results := make([]settleResponse, 3)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.settle(body) })
	}
	wg.Wait()

	settled := results[0]
	require.True(t, settled.Success, settled.ErrorReason)
	require.NotEmpty(t, settled.Transaction)
	for _, r := range results[1:] {
		assert.Equal(t, settled, r)
	}

	receipt, err := s.chain.eth.TransactionReceipt(context.Background(), common.HexToHash(settled.Transaction))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), receipt.Status)
	assert.Equal(t, int64(10000), s.chain.balance(s.token, s.payTo))
	assert.Equal(t, int64(990000), s.chain.balance(s.token, s.buyer))
	assert.Equal(t, uint64(1), s.chain.nonce(s.facilitator), "gas paid once")

	// A retry after the fact gets the recorded outcome, not "nonce already used".
	assert.Equal(t, settled, f.settle(body))
	assert.Equal(t, uint64(1), s.chain.nonce(s.facilitator))
	assert.Equal(t, int64(10000), s.chain.balance(s.token, s.payTo))

	// The authorization is spent, so the payment no longer verifies.
	assert.False(t, f.verify(body).IsValid)
}

func TestPolicyRejectsWithoutGas(t *testing.T) {
	s, f := newSetup(t)

	tests := []struct {
		name   string
		body   []byte
		reason string
	}{
		{"above maximum", s.pay("10000001"), "amount_exceeds_limit"},
		{"below minimum", s.pay("999"), "amount_below_minimum"},
		{"asset not allowed", func() []byte {
			other := s.chain.deployToken()
			key, _ := newKey(t)
			return pay(t, key, requirements(other, s.payTo, "10000"))
		}(), "asset_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verified := f.verify(tt.body)
			assert.False(t, verified.IsValid)
			assert.Equal(t, tt.reason, verified.InvalidReason)

			settled := f.settle(tt.body)
			assert.False(t, settled.Success)
			assert.Equal(t, tt.reason, settled.ErrorReason)
			assert.Empty(t, settled.Transaction)
		})
	}
	assert.Equal(t, uint64(0), s.chain.nonce(s.facilitator), "no transaction sent")
}

// A settlement whose receipt never arrives is replaced at the same nonce so it
// stops blocking the wallet. The caller sees the original hash and a failure;
// the payer is not charged.
func TestStuckSettlementIsCancelled(t *testing.T) {
	s, f := newSetup(t, "RECEIPT_TIMEOUT=2s")
	body := s.pay("10000")

	s.chain.setAutomine(false)
	settled := f.settle(body)
	require.False(t, settled.Success)
	require.NotEmpty(t, settled.Transaction, "a broadcast settlement reports its hash")

	// Mine the mempool: only the cancellation can land.
	s.chain.mine()
	_, err := s.chain.eth.TransactionReceipt(context.Background(), common.HexToHash(settled.Transaction))
	require.ErrorIs(t, err, ethereum.NotFound, "the stuck payment must not land")
	assert.Equal(t, uint64(1), s.chain.nonce(s.facilitator), "nonce consumed by the cancellation")
	assert.Equal(t, int64(0), s.chain.balance(s.token, s.payTo))
	assert.Equal(t, int64(1_000_000), s.chain.balance(s.token, s.buyer))
	assert.Equal(t, false, s.chain.tokenCall(s.token, "authorizationState", s.buyer, authorizationNonce(t, body)))

	// A retry replays the recorded failure instead of broadcasting again.
	assert.Equal(t, settled, f.settle(body))
	s.chain.mine()
	assert.Equal(t, uint64(1), s.chain.nonce(s.facilitator))

	// The wallet is unblocked: the next payment settles.
	s.chain.setAutomine(true)
	next := f.settle(s.pay("20000"))
	require.True(t, next.Success, next.ErrorReason)
	assert.Equal(t, int64(20000), s.chain.balance(s.token, s.payTo))
}
