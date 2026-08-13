package wallet

import "context"

//go:generate go tool mockgen -destination=../../mocks/domain/wallet.go -package=mocks -source=repository.go

// BalanceReader reads one settlement wallet's native balance from its chain.
// Implementations are the chain signers under internal/infra/chain.
type BalanceReader interface {
	// Balance returns the wallet's current native-currency balance.
	Balance(ctx context.Context) (Balance, error)
}

// Registry is every settlement wallet the facilitator owns, keyed by CAIP-2
// network. Built from the configured signers at startup.
type Registry map[string]BalanceReader
