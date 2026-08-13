// Package svm is the Solana half of the facilitator's chain access: it
// co-signs, simulates, broadcasts and confirms payment transactions for one
// network.
//
// The Signer here satisfies two consumers — the x402 mechanisms, through the
// SDK's FacilitatorSvmSigner interface, and the wallet use cases, through
// domain/wallet.BalanceReader.
package svm

import (
	"context"
	"fmt"
	"math/big"
	"time"

	solana "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	svmmech "github.com/x402-foundation/x402/go/v2/mechanisms/svm"

	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

// lamportsPerSOL converts the chain's atomic unit to whole SOL.
const lamportsPerSOL = 1e9

// Config configures a Solana facilitator signer.
type Config struct {
	// PrivateKey is the fee payer key in base58 form.
	PrivateKey string
	// RPCURL overrides the network default endpoint. Empty uses the default.
	RPCURL string
	// ConfirmAttempts and ConfirmDelay bound how long settlement waits for
	// confirmation. Zero values fall back to the SDK defaults.
	ConfirmAttempts int
	ConfirmDelay    time.Duration
}

// Signer implements svmmech.FacilitatorSvmSigner for a single Solana network.
//
// The facilitator is the fee payer: it co-signs the client's transaction,
// simulates it, broadcasts it and waits for confirmation.
type Signer struct {
	cfg     Config
	key     solana.PrivateKey
	client  *rpc.Client
	network string
}

var _ svmmech.FacilitatorSvmSigner = (*Signer)(nil)

// New builds a Solana signer for network.
func New(network string, cfg Config) (*Signer, error) {
	key, err := solana.PrivateKeyFromBase58(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse solana private key for %s: %w", network, err)
	}

	rpcURL := cfg.RPCURL
	if rpcURL == "" {
		networkConfig, cfgErr := svmmech.GetNetworkConfig(network)
		if cfgErr != nil {
			return nil, fmt.Errorf("resolve RPC URL for %s: %w", network, cfgErr)
		}
		rpcURL = networkConfig.RPCURL
	}
	if cfg.ConfirmAttempts <= 0 {
		cfg.ConfirmAttempts = svmmech.MaxConfirmAttempts
	}
	if cfg.ConfirmDelay <= 0 {
		cfg.ConfirmDelay = svmmech.ConfirmRetryDelay
	}

	return &Signer{
		cfg:     cfg,
		key:     key,
		client:  rpc.New(rpcURL),
		network: network,
	}, nil
}

// Network returns the CAIP-2 network this signer settles on.
func (s *Signer) Network() string { return s.network }

// Address returns the fee payer address in base58 form.
func (s *Signer) Address() string { return s.key.PublicKey().String() }

// GetAddresses returns the fee payer addresses available for network.
func (s *Signer) GetAddresses(context.Context, string) []solana.PublicKey {
	return []solana.PublicKey{s.key.PublicKey()}
}

// SignTransaction adds the facilitator's fee-payer signature in place.
func (s *Signer) SignTransaction(_ context.Context, tx *solana.Transaction, feePayer solana.PublicKey, _ string) error {
	if !feePayer.Equals(s.key.PublicKey()) {
		return fmt.Errorf("no signer for fee payer %s (have %s)", feePayer, s.key.PublicKey())
	}

	message, err := tx.Message.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	signature, err := s.key.Sign(message)
	if err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	index, err := tx.GetAccountIndex(s.key.PublicKey())
	if err != nil {
		return fmt.Errorf("locate signer account: %w", err)
	}
	if len(tx.Signatures) <= int(index) {
		grown := make([]solana.Signature, index+1)
		copy(grown, tx.Signatures)
		tx.Signatures = grown
	}
	tx.Signatures[index] = signature
	return nil
}

// SimulateTransaction checks the transaction would succeed before broadcasting.
func (s *Signer) SimulateTransaction(ctx context.Context, tx *solana.Transaction, _ string) error {
	result, err := s.client.SimulateTransactionWithOpts(ctx, tx, &rpc.SimulateTransactionOpts{
		SigVerify:              true,
		ReplaceRecentBlockhash: false,
		Commitment:             svmmech.DefaultCommitment,
	})
	if err != nil {
		return fmt.Errorf("simulate transaction: %w", err)
	}
	if result != nil && result.Value != nil && result.Value.Err != nil {
		return fmt.Errorf("simulation failed: %v", result.Value.Err)
	}
	return nil
}

// SendTransaction broadcasts the co-signed transaction.
//
// Preflight is skipped because SimulateTransaction already ran; re-simulating
// on send would double the RPC cost of every settlement.
func (s *Signer) SendTransaction(ctx context.Context, tx *solana.Transaction, _ string) (solana.Signature, error) {
	signature, err := s.client.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		SkipPreflight:       true,
		PreflightCommitment: svmmech.DefaultCommitment,
	})
	if err != nil {
		return solana.Signature{}, fmt.Errorf("send transaction: %w", err)
	}
	return signature, nil
}

// ConfirmTransaction polls until the transaction reaches the configured
// commitment level, fails on-chain, or the attempt budget is exhausted.
func (s *Signer) ConfirmTransaction(ctx context.Context, signature solana.Signature, _ string) error {
	for attempt := 0; attempt < s.cfg.ConfirmAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		statuses, err := s.client.GetSignatureStatuses(ctx, true, signature)
		if err == nil && statuses != nil && len(statuses.Value) > 0 && statuses.Value[0] != nil {
			status := statuses.Value[0]
			if status.Err != nil {
				return fmt.Errorf("transaction %s failed on-chain: %v", signature, status.Err)
			}
			if status.ConfirmationStatus == rpc.ConfirmationStatusConfirmed ||
				status.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.cfg.ConfirmDelay):
		}
	}
	return fmt.Errorf("transaction %s not confirmed after %d attempts", signature, s.cfg.ConfirmAttempts)
}

// Balance returns the fee payer's SOL balance, implementing
// domain/wallet.BalanceReader for the gas monitor.
func (s *Signer) Balance(ctx context.Context) (wallet.Balance, error) {
	result, err := s.client.GetBalance(ctx, s.key.PublicKey(), svmmech.DefaultCommitment)
	if err != nil {
		return wallet.Balance{}, fmt.Errorf("read SOL balance: %w", err)
	}
	if result == nil {
		return wallet.Balance{}, fmt.Errorf("empty balance response")
	}

	return wallet.Balance{
		Network:     s.network,
		Address:     s.Address(),
		Atomic:      new(big.Int).SetUint64(result.Value),
		UnitDivisor: lamportsPerSOL,
	}, nil
}
