package svm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	mainnetGenesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	devnetGenesis  = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
)

func Test_checkGenesis(t *testing.T) {
	tests := []struct {
		name    string
		network string
		genesis string
		wantErr bool
	}{
		{
			name:    "mainnet RPC for the mainnet network",
			network: "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
			genesis: mainnetGenesis,
		},
		{
			name:    "devnet RPC for the devnet network",
			network: "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1",
			genesis: devnetGenesis,
		},
		{
			// The case the check exists for: settlements would be broadcast to
			// a cluster the payer never signed for.
			name:    "devnet RPC configured for mainnet",
			network: "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
			genesis: devnetGenesis,
			wantErr: true,
		},
		{
			// An empty reference is a prefix of every hash; it must not pass.
			name:    "a network id with no reference",
			network: "solana:",
			genesis: mainnetGenesis,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkGenesis(tt.network, tt.genesis)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
