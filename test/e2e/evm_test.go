//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	evmclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
	"github.com/x402-foundation/x402/go/v2/types"
)

const evmNetwork = "eip155:31337" // anvil's default chain

var (
	tokenABI abi.ABI
	tokenBin []byte
)

func compileToken() error {
	out, err := exec.Command("solc", "--optimize", "--combined-json", "abi,bin", "testdata/TestToken.sol").Output()
	if err != nil {
		return fmt.Errorf("compile TestToken.sol (is solc on PATH? run inside devbox): %w", err)
	}
	var compiled struct {
		Contracts map[string]struct {
			ABI json.RawMessage `json:"abi"`
			Bin string          `json:"bin"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(out, &compiled); err != nil {
		return fmt.Errorf("parse solc output: %w", err)
	}
	contract, ok := compiled.Contracts["testdata/TestToken.sol:TestToken"]
	if !ok {
		return errors.New("solc output has no TestToken")
	}
	if tokenABI, err = abi.JSON(bytes.NewReader(contract.ABI)); err != nil {
		return fmt.Errorf("parse TestToken ABI: %w", err)
	}
	tokenBin = common.FromHex(contract.Bin)
	return nil
}

// ── Tests ────────────────────────────────────────────────────────────────────

// evmSetup is one anvil chain with a token, a facilitator wallet with gas, a
// buyer holding 1 token, and a seller.
type evmSetup struct {
	chain       *chain
	token       common.Address
	facilitator common.Address
	buyer       common.Address
	payTo       common.Address
	pay         func(amount string) []byte
}

func newEVMSetup(t *testing.T, extraEnv ...string) (*evmSetup, *facilitator) {
	t.Helper()
	c := startChain(t)
	token := c.deployToken()
	key, facilitatorAddr := c.newAccount()
	buyerKey, buyer := newEVMKey(t)
	_, payTo := newEVMKey(t)
	c.mint(token, buyer, 1_000_000)

	s := &evmSetup{
		chain:       c,
		token:       token,
		facilitator: facilitatorAddr,
		buyer:       buyer,
		payTo:       payTo,
		pay: func(amount string) []byte {
			return evmPay(t, buyerKey, evmRequirements(token, payTo, amount))
		},
	}
	env := append([]string{
		"EVM_RPC_URLS=" + evmNetwork + "=" + c.url,
		"EVM_PRIVATE_KEY=" + hex.EncodeToString(crypto.FromECDSA(key)),
		"ALLOWED_ASSETS=" + evmNetwork + "/" + token.Hex() + "=1000:10000000",
	}, extraEnv...)
	return s, startFacilitator(t, env...)
}

func TestEVMSettle(t *testing.T) {
	s, f := newEVMSetup(t)
	body := s.pay("10000")

	verified := f.verify(body)
	require.True(t, verified.IsValid, verified.InvalidReason)
	assert.True(t, strings.EqualFold(s.buyer.Hex(), verified.Payer))

	// Concurrent retries of one payment: one transaction, every caller gets its hash.
	results := f.settleConcurrently(body, 3)
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

func TestEVMPolicyRejectsWithoutGas(t *testing.T) {
	s, f := newEVMSetup(t)

	tests := []struct {
		name   string
		body   []byte
		reason string
	}{
		{"above maximum", s.pay("10000001"), "amount_exceeds_limit"},
		{"below minimum", s.pay("999"), "amount_below_minimum"},
		{"asset not allowed", func() []byte {
			other := s.chain.deployToken()
			key, _ := newEVMKey(t)
			return evmPay(t, key, evmRequirements(other, s.payTo, "10000"))
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
func TestEVMStuckSettlementIsCancelled(t *testing.T) {
	s, f := newEVMSetup(t, "RECEIPT_TIMEOUT=2s")
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

// ── Chain ────────────────────────────────────────────────────────────────────

// chain is one anvil instance. Its prefunded, unlocked account deploys and
// mints; the accounts under test get fresh keys.
type chain struct {
	t      *testing.T
	url    string
	rpc    *rpc.Client
	eth    *ethclient.Client
	funder common.Address
}

func startChain(t *testing.T) *chain {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command("anvil", "--port", port, "--silent")
	require.NoError(t, cmd.Start(), "start anvil (is it on PATH? run inside devbox)")
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	url := "http://127.0.0.1:" + port
	client, err := rpc.Dial(url)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	c := &chain{t: t, url: url, rpc: client, eth: ethclient.NewClient(client)}
	waitFor(t, 10*time.Second, "anvil did not come up", func() bool {
		_, err := c.eth.ChainID(context.Background())
		return err == nil
	})

	var accounts []common.Address
	c.do(&accounts, "eth_accounts")
	c.funder = accounts[0]
	return c
}

func (c *chain) do(result any, method string, args ...any) {
	c.t.Helper()
	require.NoError(c.t, c.rpc.CallContext(context.Background(), result, method, args...), method)
}

// newAccount returns a fresh key holding 10 ETH for gas.
func (c *chain) newAccount() (*ecdsa.PrivateKey, common.Address) {
	c.t.Helper()
	key, addr := newEVMKey(c.t)
	c.do(nil, "anvil_setBalance", addr, hexutil.EncodeBig(new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18))))
	return key, addr
}

// send executes a transaction from the funder and returns its receipt. It
// relies on automine.
func (c *chain) send(to *common.Address, data []byte) *gethtypes.Receipt {
	c.t.Helper()
	tx := map[string]any{"from": c.funder, "data": hexutil.Bytes(data)}
	if to != nil {
		tx["to"] = *to
	}
	var hash common.Hash
	c.do(&hash, "eth_sendTransaction", tx)
	var receipt *gethtypes.Receipt
	waitFor(c.t, 5*time.Second, "transaction not mined", func() bool {
		var err error
		receipt, err = c.eth.TransactionReceipt(context.Background(), hash)
		return err == nil
	})
	require.Equal(c.t, gethtypes.ReceiptStatusSuccessful, receipt.Status)
	return receipt
}

func (c *chain) deployToken() common.Address {
	c.t.Helper()
	return c.send(nil, tokenBin).ContractAddress
}

func (c *chain) mint(token, to common.Address, amount int64) {
	c.t.Helper()
	data, err := tokenABI.Pack("mint", to, big.NewInt(amount))
	require.NoError(c.t, err)
	c.send(&token, data)
}

func (c *chain) tokenCall(token common.Address, method string, args ...any) any {
	c.t.Helper()
	data, err := tokenABI.Pack(method, args...)
	require.NoError(c.t, err)
	out, err := c.eth.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	require.NoError(c.t, err)
	values, err := tokenABI.Unpack(method, out)
	require.NoError(c.t, err)
	return values[0]
}

func (c *chain) balance(token, holder common.Address) int64 {
	c.t.Helper()
	return c.tokenCall(token, "balanceOf", holder).(*big.Int).Int64()
}

// nonce is the account's mined transaction count.
func (c *chain) nonce(addr common.Address) uint64 {
	c.t.Helper()
	n, err := c.eth.NonceAt(context.Background(), addr, nil)
	require.NoError(c.t, err)
	return n
}

// setAutomine off leaves transactions in the mempool until mine is called.
func (c *chain) setAutomine(on bool) { c.do(nil, "evm_setAutomine", on) }

func (c *chain) mine() { c.do(nil, "evm_mine") }

// ── Payments ─────────────────────────────────────────────────────────────────

func evmRequirements(token, payTo common.Address, amount string) types.PaymentRequirements {
	return types.PaymentRequirements{
		Scheme:            "exact",
		Network:           evmNetwork,
		Asset:             token.Hex(),
		Amount:            amount,
		PayTo:             payTo.Hex(),
		MaxTimeoutSeconds: 60,
		Extra:             map[string]any{"name": "USD Coin", "version": "2"},
	}
}

// evmPay signs an EIP-3009 authorization for req with the SDK's client.
func evmPay(t *testing.T, buyer *ecdsa.PrivateKey, req types.PaymentRequirements) []byte {
	t.Helper()
	signer, err := evmsigners.NewClientSignerFromPrivateKey(hex.EncodeToString(crypto.FromECDSA(buyer)))
	require.NoError(t, err)
	payload, err := evmclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(context.Background(), req)
	require.NoError(t, err)
	return paymentBody(t, payload, req)
}

// authorizationNonce reads the EIP-3009 nonce back out of a payment body.
func authorizationNonce(t *testing.T, body []byte) common.Hash {
	t.Helper()
	var doc struct {
		PaymentPayload struct {
			Payload struct {
				Authorization struct {
					Nonce string `json:"nonce"`
				} `json:"authorization"`
			} `json:"payload"`
		} `json:"paymentPayload"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	return common.HexToHash(doc.PaymentPayload.Payload.Authorization.Nonce)
}

func newEVMKey(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	return key, crypto.PubkeyToAddress(key.PublicKey)
}
