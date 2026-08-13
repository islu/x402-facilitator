package evm

import (
	"context"

	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

// Signers is every configured EVM settlement wallet, keyed by CAIP-2 network.
type Signers map[string]*Signer

// ProvideSigners dials one signer per configured EVM network.
//
// Every endpoint is contacted here — the chain ID check happens during dial —
// so a bad RPC URL or key fails at startup rather than on the first payment.
// The returned cleanup closes every connection, including those already opened
// when a later network fails.
func ProvideSigners(ctx context.Context, cfg *config.Config) (Signers, func(), error) {
	signers := Signers{}
	cleanup := func() {
		for _, signer := range signers {
			signer.Close()
		}
	}

	for _, network := range cfg.EVMNetworks {
		signer, err := New(ctx, network.Network, Config{
			PrivateKey:      network.PrivateKey,
			RPCURL:          network.RPCURL,
			ExpectedChainID: network.ChainID,
			MaxFeePerGasWei: cfg.MaxFeePerGasWei,
			GasLimit:        cfg.GasLimit,
			ReceiptTimeout:  cfg.ReceiptTimeout,
		})
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		signers[network.Network] = signer
	}

	return signers, cleanup, nil
}

// Wallets exposes the signers as balance readers for the wallet use cases.
func (s Signers) Wallets() wallet.Registry {
	registry := make(wallet.Registry, len(s))
	for network, signer := range s {
		registry[network] = signer
	}
	return registry
}
