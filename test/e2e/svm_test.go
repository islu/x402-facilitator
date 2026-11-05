//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"

	solana "github.com/gagliardetto/solana-go"
	ata "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/x402-foundation/x402/go/v2/mechanisms/svm"
	svmclient "github.com/x402-foundation/x402/go/v2/mechanisms/svm/exact/client"
	svmsigners "github.com/x402-foundation/x402/go/v2/signers/svm"
	"github.com/x402-foundation/x402/go/v2/types"
)

// Solana has no EVM-style nonce queue: an unconfirmed transaction expires with
// its blockhash instead of blocking later ones, so there is no stuck-transaction
// case here.

// svmSetup is one validator with a mint, a funded fee payer, a buyer holding 1
// token, and a seller with a token account. otherMint is not allowed.
type svmSetup struct {
	validator *validator
	mint      solana.PublicKey
	otherMint solana.PublicKey
	feePayer  solana.PublicKey
	buyer     solana.PublicKey
	payTo     solana.PublicKey
	pay       func(amount string) []byte
}

func newSVMSetup(t *testing.T) (*svmSetup, *facilitator) {
	t.Helper()
	v := startValidator(t)
	authority := v.newAccount()
	feePayer := v.newAccount()
	buyer := solana.NewWallet().PrivateKey
	payTo := solana.NewWallet().PublicKey()

	mint := v.createMint(authority)
	v.createTokenAccount(authority, buyer.PublicKey(), mint)
	v.createTokenAccount(authority, payTo, mint)
	v.mintTo(authority, mint, buyer.PublicKey(), 1_000_000)
	otherMint := v.createMint(authority)
	// The SDK's client reads the mint and blockhash at finalized commitment.
	v.waitFinalized()

	s := &svmSetup{
		validator: v,
		mint:      mint,
		otherMint: otherMint,
		feePayer:  feePayer.PublicKey(),
		buyer:     buyer.PublicKey(),
		payTo:     payTo,
	}
	s.pay = func(amount string) []byte {
		return v.pay(t, buyer, s.requirements(mint, amount))
	}
	return s, startFacilitator(t,
		"SVM_RPC_URLS="+v.network+"="+v.url,
		"SVM_PRIVATE_KEY="+feePayer.String(),
		"ALLOWED_ASSETS="+v.network+"/"+mint.String()+"=1000:10000000",
	)
}

func (s *svmSetup) requirements(mint solana.PublicKey, amount string) types.PaymentRequirements {
	return types.PaymentRequirements{
		Scheme:            "exact",
		Network:           s.validator.network,
		Asset:             mint.String(),
		Amount:            amount,
		PayTo:             s.payTo.String(),
		MaxTimeoutSeconds: 60,
		Extra:             map[string]any{"feePayer": s.feePayer.String()},
	}
}

func TestSVMSettle(t *testing.T) {
	s, f := newSVMSetup(t)
	v := s.validator
	body := s.pay("10000")

	verified := f.verify(body)
	require.True(t, verified.IsValid, verified.InvalidReason)
	assert.Equal(t, s.buyer.String(), verified.Payer)

	before := v.signatureCount(s.feePayer)

	// Concurrent retries of one payment: one transaction, every caller gets its signature.
	results := f.settleConcurrently(body, 3)
	settled := results[0]
	require.True(t, settled.Success, settled.ErrorReason)
	require.NotEmpty(t, settled.Transaction)
	for _, r := range results[1:] {
		assert.Equal(t, settled, r)
	}

	assert.Equal(t, uint64(10000), v.tokenBalance(s.payTo, s.mint))
	assert.Equal(t, uint64(990000), v.tokenBalance(s.buyer, s.mint))
	assert.Equal(t, before+1, v.signatureCount(s.feePayer), "fees paid once")

	// A retry after the fact gets the recorded outcome, not "duplicate settlement".
	assert.Equal(t, settled, f.settle(body))
	assert.Equal(t, before+1, v.signatureCount(s.feePayer))
	assert.Equal(t, uint64(10000), v.tokenBalance(s.payTo, s.mint))
}

func TestSVMPolicyRejectsWithoutFees(t *testing.T) {
	s, f := newSVMSetup(t)
	v := s.validator

	tests := []struct {
		name   string
		body   []byte
		reason string
	}{
		{"above maximum", s.pay("10000001"), "amount_exceeds_limit"},
		{"below minimum", s.pay("999"), "amount_below_minimum"},
		{"asset not allowed", v.pay(t, solana.NewWallet().PrivateKey, s.requirements(s.otherMint, "10000")), "asset_not_allowed"},
	}
	before := v.signatureCount(s.feePayer)
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
	assert.Equal(t, before, v.signatureCount(s.feePayer), "no transaction sent")
}

// ── Validator ────────────────────────────────────────────────────────────────

// validator is one solana-test-validator with a fresh ledger. Its genesis hash
// is new every run, so the network id is derived from it.
type validator struct {
	t       *testing.T
	url     string
	network string
	rpc     *rpc.Client
}

func startValidator(t *testing.T) *validator {
	t.Helper()
	rpcPort := freePort(t)
	cmd := exec.Command("solana-test-validator",
		"--ledger", t.TempDir(),
		"--rpc-port", rpcPort,
		"--faucet-port", freePort(t),
		"--gossip-port", freePort(t),
		"--bind-address", "127.0.0.1",
		"--quiet", "--reset",
	)
	require.NoError(t, cmd.Start(), "start solana-test-validator (is it on PATH? run inside devbox)")
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	v := &validator{t: t, url: "http://127.0.0.1:" + rpcPort, rpc: rpc.New("http://127.0.0.1:" + rpcPort)}
	var genesis solana.Hash
	waitFor(t, 30*time.Second, "validator did not come up", func() bool {
		var err error
		genesis, err = v.rpc.GetGenesisHash(context.Background())
		return err == nil
	})
	v.network = "solana:" + genesis.String()[:32]

	// The SDK's client builds payments only for the clusters it knows; teach it
	// this one. The facilitator itself accepts any cluster it is configured for.
	svm.NetworkConfigs[v.network] = svm.NetworkConfig{Name: "localnet", CAIP2: v.network, RPCURL: v.url}
	t.Cleanup(func() { delete(svm.NetworkConfigs, v.network) })
	return v
}

// newAccount returns a fresh key holding 10 SOL.
func (v *validator) newAccount() solana.PrivateKey {
	v.t.Helper()
	key := solana.NewWallet().PrivateKey
	var sig solana.Signature
	// The faucet starts a moment after the RPC.
	waitFor(v.t, 15*time.Second, "airdrop failed", func() bool {
		var err error
		sig, err = v.rpc.RequestAirdrop(context.Background(), key.PublicKey(), 10*solana.LAMPORTS_PER_SOL, rpc.CommitmentConfirmed)
		return err == nil
	})
	v.confirm(sig)
	return key
}

// send signs with payer (who pays the fee) and extra signers, then waits for
// confirmation.
func (v *validator) send(payer solana.PrivateKey, extra []solana.PrivateKey, instructions ...solana.Instruction) {
	v.t.Helper()
	ctx := context.Background()
	recent, err := v.rpc.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	require.NoError(v.t, err)
	tx, err := solana.NewTransaction(instructions, recent.Value.Blockhash, solana.TransactionPayer(payer.PublicKey()))
	require.NoError(v.t, err)

	signers := append([]solana.PrivateKey{payer}, extra...)
	_, err = tx.Sign(func(pub solana.PublicKey) *solana.PrivateKey {
		for i := range signers {
			if signers[i].PublicKey().Equals(pub) {
				return &signers[i]
			}
		}
		return nil
	})
	require.NoError(v.t, err)

	sig, err := v.rpc.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{PreflightCommitment: rpc.CommitmentConfirmed})
	require.NoError(v.t, err)
	v.confirm(sig)
}

func (v *validator) confirm(sig solana.Signature) {
	v.t.Helper()
	waitFor(v.t, 30*time.Second, "transaction "+sig.String()+" not confirmed", func() bool {
		out, err := v.rpc.GetSignatureStatuses(context.Background(), false, sig)
		if err != nil || len(out.Value) == 0 || out.Value[0] == nil {
			return false
		}
		status := out.Value[0]
		require.Nil(v.t, status.Err, "transaction failed")
		return status.ConfirmationStatus == rpc.ConfirmationStatusConfirmed ||
			status.ConfirmationStatus == rpc.ConfirmationStatusFinalized
	})
}

// waitFinalized waits until everything confirmed so far is finalized, about
// 32 slots (~13s) on a local validator.
func (v *validator) waitFinalized() {
	v.t.Helper()
	ctx := context.Background()
	target, err := v.rpc.GetSlot(ctx, rpc.CommitmentConfirmed)
	require.NoError(v.t, err)
	waitFor(v.t, 60*time.Second, "slots not finalized", func() bool {
		slot, err := v.rpc.GetSlot(ctx, rpc.CommitmentFinalized)
		return err == nil && slot >= target
	})
}

// createMint creates a 6-decimal SPL mint controlled by authority.
func (v *validator) createMint(authority solana.PrivateKey) solana.PublicKey {
	v.t.Helper()
	mint := solana.NewWallet().PrivateKey
	rent, err := v.rpc.GetMinimumBalanceForRentExemption(context.Background(), token.MINT_SIZE, rpc.CommitmentConfirmed)
	require.NoError(v.t, err)
	v.send(authority, []solana.PrivateKey{mint},
		system.NewCreateAccountInstruction(rent, token.MINT_SIZE, solana.TokenProgramID, authority.PublicKey(), mint.PublicKey()).Build(),
		token.NewInitializeMint2Instruction(6, authority.PublicKey(), authority.PublicKey(), mint.PublicKey()).Build(),
	)
	return mint.PublicKey()
}

func (v *validator) createTokenAccount(payer solana.PrivateKey, owner, mint solana.PublicKey) {
	v.t.Helper()
	v.send(payer, nil, ata.NewCreateInstruction(payer.PublicKey(), owner, mint).Build())
}

func (v *validator) mintTo(authority solana.PrivateKey, mint, owner solana.PublicKey, amount uint64) {
	v.t.Helper()
	account := tokenAccount(v.t, owner, mint)
	v.send(authority, nil, token.NewMintToInstruction(amount, mint, account, authority.PublicKey(), nil).Build())
}

func (v *validator) tokenBalance(owner, mint solana.PublicKey) uint64 {
	v.t.Helper()
	out, err := v.rpc.GetTokenAccountBalance(context.Background(), tokenAccount(v.t, owner, mint), rpc.CommitmentConfirmed)
	require.NoError(v.t, err)
	amount, err := strconv.ParseUint(out.Value.Amount, 10, 64)
	require.NoError(v.t, err)
	return amount
}

// signatureCount is how many confirmed transactions involve addr; for the fee
// payer, every one of them cost it a fee.
func (v *validator) signatureCount(addr solana.PublicKey) int {
	v.t.Helper()
	sigs, err := v.rpc.GetSignaturesForAddressWithOpts(context.Background(), addr, &rpc.GetSignaturesForAddressOpts{Commitment: rpc.CommitmentConfirmed})
	require.NoError(v.t, err)
	return len(sigs)
}

// pay builds and signs the buyer's transfer with the SDK's client.
func (v *validator) pay(t *testing.T, buyer solana.PrivateKey, req types.PaymentRequirements) []byte {
	t.Helper()
	signer, err := svmsigners.NewClientSignerFromPrivateKey(buyer.String())
	require.NoError(t, err)
	payload, err := svmclient.NewExactSvmScheme(signer, &svm.ClientConfig{RPCURL: v.url}).CreatePaymentPayload(context.Background(), req)
	require.NoError(t, err)
	return paymentBody(t, payload, req)
}

func tokenAccount(t *testing.T, owner, mint solana.PublicKey) solana.PublicKey {
	t.Helper()
	account, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	require.NoError(t, err)
	return account
}
