package svm

import (
	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

// Signers is every configured Solana settlement wallet, keyed by CAIP-2 network.
type Signers map[string]*Signer

// ProvideSigners builds one signer per configured Solana network. Solana RPC
// clients are stateless HTTP clients, so there is nothing to close.
func ProvideSigners(cfg *config.Config) (Signers, error) {
	signers := Signers{}

	for _, network := range cfg.SVMNetworks {
		signer, err := New(network.Network, Config{
			PrivateKey: network.PrivateKey,
			RPCURL:     network.RPCURL,
		})
		if err != nil {
			return nil, err
		}
		signers[network.Network] = signer
	}

	return signers, nil
}

// Wallets exposes the signers as balance readers for the wallet use cases.
func (s Signers) Wallets() wallet.Registry {
	registry := make(wallet.Registry, len(s))
	for network, signer := range s {
		registry[network] = signer
	}
	return registry
}
