//go:build e2e

// Package e2e runs the facilitator binary against a local anvil chain: real
// HTTP, real transactions, and an EIP-3009 token compiled from testdata.
//
// Run it with `task e2e`; it needs anvil and solc, which devbox provides. Each
// test starts its own chain and facilitator, so tests share no state.
package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
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
	"github.com/stretchr/testify/require"
	evmclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
	"github.com/x402-foundation/x402/go/v2/types"
)

const network = "eip155:31337" // anvil's default chain

var (
	facilitatorBin string
	tokenABI       abi.ABI
	tokenBin       []byte
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "x402-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	facilitatorBin = filepath.Join(dir, "facilitator")
	if out, err := exec.Command("go", "build", "-o", facilitatorBin, "../../cmd/facilitator").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build facilitator: %v\n%s", err, out)
		return 1
	}
	if err := compileToken(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

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
		return fmt.Errorf("solc output has no TestToken")
	}
	if tokenABI, err = abi.JSON(bytes.NewReader(contract.ABI)); err != nil {
		return fmt.Errorf("parse TestToken ABI: %w", err)
	}
	tokenBin = common.FromHex(contract.Bin)
	return nil
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
	require.Eventually(t, func() bool {
		_, err := c.eth.ChainID(context.Background())
		return err == nil
	}, 10*time.Second, 50*time.Millisecond, "anvil did not come up")

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
	key, addr := newKey(c.t)
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
	require.Eventually(c.t, func() bool {
		var err error
		receipt, err = c.eth.TransactionReceipt(context.Background(), hash)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond, "transaction %s not mined", hash)
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

// ── Facilitator ──────────────────────────────────────────────────────────────

type facilitator struct {
	t   *testing.T
	url string
}

// startFacilitator runs the binary for one network, settling with key and
// allowing token at 0.001–10 units. extraEnv overrides the defaults.
func startFacilitator(t *testing.T, c *chain, key *ecdsa.PrivateKey, token common.Address, extraEnv ...string) *facilitator {
	t.Helper()
	port, adminPort := freePort(t), freePort(t)
	cmd := exec.Command(facilitatorBin)
	// An empty working directory keeps a developer's .env out of the test.
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"PORT=" + port,
		"ADMIN_PORT=" + adminPort,
		"LOG_JSON=false",
		"LOG_LEVEL=debug",
		"ENABLE_V1=false",
		"BALANCE_CHECK_INTERVAL=1h",
		"EVM_RPC_URLS=" + network + "=" + c.url,
		"EVM_PRIVATE_KEY=" + hex.EncodeToString(crypto.FromECDSA(key)),
		"ALLOWED_ASSETS=" + network + "/" + token.Hex() + "=1000:10000000",
	}, extraEnv...)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-exited
		if t.Failed() {
			t.Logf("facilitator log:\n%s", logs.String())
		}
	})

	f := &facilitator{t: t, url: "http://127.0.0.1:" + port}
	deadline := time.After(15 * time.Second)
	for !f.healthy() {
		select {
		case err := <-exited:
			exited <- err // for the cleanup
			t.Fatalf("facilitator exited at startup: %v", err)
		case <-deadline:
			t.Fatal("facilitator did not come up")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return f
}

func (f *facilitator) healthy() bool {
	resp, err := http.Get(f.url + "/healthz")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type verifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason"`
	Payer         string `json:"payer"`
}

type settleResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
}

func (f *facilitator) verify(body []byte) verifyResponse {
	var out verifyResponse
	f.post("/verify", body, &out)
	return out
}

func (f *facilitator) settle(body []byte) settleResponse {
	var out settleResponse
	f.post("/settle", body, &out)
	return out
}

// post requires a 200: an invalid payment is still a successful answer.
func (f *facilitator) post(path string, body []byte, out any) {
	resp, err := http.Post(f.url+path, "application/json", bytes.NewReader(body))
	require.NoError(f.t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(f.t, http.StatusOK, resp.StatusCode, path)
	require.NoError(f.t, json.NewDecoder(resp.Body).Decode(out))
}

// ── Payments ─────────────────────────────────────────────────────────────────

func requirements(token, payTo common.Address, amount string) types.PaymentRequirements {
	return types.PaymentRequirements{
		Scheme:            "exact",
		Network:           network,
		Asset:             token.Hex(),
		Amount:            amount,
		PayTo:             payTo.Hex(),
		MaxTimeoutSeconds: 60,
		Extra:             map[string]any{"name": "USD Coin", "version": "2"},
	}
}

// pay signs an EIP-3009 authorization for req with the SDK's client and
// returns the body a resource server would send to /verify and /settle.
func pay(t *testing.T, buyer *ecdsa.PrivateKey, req types.PaymentRequirements) []byte {
	t.Helper()
	signer, err := evmsigners.NewClientSignerFromPrivateKey(hex.EncodeToString(crypto.FromECDSA(buyer)))
	require.NoError(t, err)
	payload, err := evmclient.NewExactEvmScheme(signer, nil).CreatePaymentPayload(context.Background(), req)
	require.NoError(t, err)
	payload.Accepted = req

	body, err := json.Marshal(map[string]any{
		"x402Version":         2,
		"paymentPayload":      payload,
		"paymentRequirements": req,
	})
	require.NoError(t, err)
	return body
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

// ── Helpers ──────────────────────────────────────────────────────────────────

func newKey(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	return key, crypto.PubkeyToAddress(key.PublicKey)
}

// freePort asks the OS for a port that is free right now. Another process
// could take it before it is used; on a test machine that is rare enough.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}
