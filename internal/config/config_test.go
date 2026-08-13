package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envKeys are cleared before each case so a variable inherited from the
// developer's shell cannot satisfy the configuration under test.
var envKeys = []string{
	"EVM_RPC_URLS", "SVM_RPC_URLS",
	"EVM_PRIVATE_KEY", "SVM_PRIVATE_KEY",
	"EVM_PRIVATE_KEYS", "SVM_PRIVATE_KEYS",
	"EVM_V1_ALIASES", "SVM_V1_ALIASES",
	"ENABLE_V1", "MAX_GAS_PRICE_GWEI", "MAX_SETTLE_AMOUNT",
	"VERIFY_TIMEOUT", "SETTLE_TIMEOUT", "RECEIPT_TIMEOUT",
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, key := range envKeys {
		if _, set := env[key]; !set {
			t.Setenv(key, "")
		}
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
}

func Test_Load(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		assert func(*testing.T, *Config)
	}{
		{
			name: "an EVM network derives its chain ID from the CAIP-2 reference",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc123",
			},
			assert: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.EVMNetworks, 1)
				network := cfg.EVMNetworks[0]
				assert.Equal(t, int64(84532), network.ChainID.Int64())
				assert.Equal(t, "0xabc123", network.PrivateKey)
				assert.Equal(t, "base-sepolia", network.V1Alias)
			},
		},
		{
			// One wallet per network is the recommended setup, so a compromised
			// testnet key cannot spend mainnet funds.
			name: "per-network keys override the default",
			env: map[string]string{
				"EVM_RPC_URLS":     "eip155:8453=https://mainnet.base.org,eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY":  "0xdefault",
				"EVM_PRIVATE_KEYS": "eip155:8453=0xmainnet",
			},
			assert: func(t *testing.T, cfg *Config) {
				keys := map[string]string{}
				for _, network := range cfg.EVMNetworks {
					keys[network.Network] = network.PrivateKey
				}
				assert.Equal(t, "0xmainnet", keys["eip155:8453"])
				assert.Equal(t, "0xdefault", keys["eip155:84532"])
			},
		},
		{
			// RPC URLs carry query strings, so only the first separator counts.
			name: "an RPC URL keeps its query string",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://rpc.example/v2?apiKey=k=v",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			assert: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "https://rpc.example/v2?apiKey=k=v", cfg.EVMNetworks[0].RPCURL)
			},
		},
		{
			name: "V1 registration can be disabled",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
				"ENABLE_V1":       "false",
			},
			assert: func(t *testing.T, cfg *Config) {
				assert.Empty(t, cfg.EVMNetworks[0].V1Alias)
			},
		},
		{
			name: "a V1 alias can be overridden per network",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
				"EVM_V1_ALIASES":  "eip155:84532=custom-testnet",
			},
			assert: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "custom-testnet", cfg.EVMNetworks[0].V1Alias)
			},
		},
		{
			name: "a Solana network is configured independently",
			env: map[string]string{
				"SVM_RPC_URLS":    "solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1=https://api.devnet.solana.com",
				"SVM_PRIVATE_KEY": "base58key",
			},
			assert: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.SVMNetworks, 1)
				assert.Equal(t, "solana-devnet", cfg.SVMNetworks[0].V1Alias)
				assert.Empty(t, cfg.EVMNetworks)
			},
		},
		{
			name: "the gas ceiling is converted from gwei to wei",
			env: map[string]string{
				"EVM_RPC_URLS":       "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY":    "0xabc",
				"MAX_GAS_PRICE_GWEI": "100",
			},
			assert: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "100000000000", cfg.MaxFeePerGasWei.String())
			},
		},
		{
			name: "an unset gas ceiling and settlement limit stay nil",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			assert: func(t *testing.T, cfg *Config) {
				assert.Nil(t, cfg.MaxFeePerGasWei, "uncapped gas price")
				assert.Nil(t, cfg.MaxSettleAmount, "no settlement ceiling")
				assert.NotNil(t, cfg.MinBalanceWei, "the balance warning has a default")
			},
		},
		{
			name: "networks are ordered deterministically",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org,eip155:1=https://eth.example,eip155:8453=https://mainnet.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			assert: func(t *testing.T, cfg *Config) {
				// Sorted as strings, so eip155:8453 precedes its longer prefix
				// eip155:84532 — stable is what matters, not numeric order.
				assert.Equal(t, []string{"eip155:1", "eip155:8453", "eip155:84532"}, cfg.Networks())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)

			cfg, err := Load()

			require.NoError(t, err)
			tt.assert(t, cfg)
		})
	}
}

// Configuration problems must stop the boot, not surface on the first payment.
func Test_Load_Errors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "no networks configured",
			env:     map[string]string{},
			wantErr: "no networks configured",
		},
		{
			name:    "a network without a key",
			env:     map[string]string{"EVM_RPC_URLS": "eip155:84532=https://sepolia.base.org"},
			wantErr: "no private key",
		},
		{
			name: "a legacy network name in the EVM list",
			env: map[string]string{
				"EVM_RPC_URLS":    "base-sepolia=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			wantErr: "not an eip155 CAIP-2 network",
		},
		{
			name: "an EVM network id with a non-numeric reference",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:base=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			wantErr: "invalid chain ID reference",
		},
		{
			name: "a non-solana network in the SVM list",
			env: map[string]string{
				"SVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"SVM_PRIVATE_KEY": "key",
			},
			wantErr: "not a solana CAIP-2 network",
		},
		{
			name: "a malformed network map",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			wantErr: "not in network=value form",
		},
		{
			name: "a duplicate network entry",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://a.example,eip155:84532=https://b.example",
				"EVM_PRIVATE_KEY": "0xabc",
			},
			wantErr: "duplicate entry",
		},
		{
			// A receipt wait longer than the request budget would have gin cut
			// off a settlement that already broadcast.
			name: "a receipt timeout at or above the settle timeout",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
				"SETTLE_TIMEOUT":  "30s",
				"RECEIPT_TIMEOUT": "60s",
			},
			wantErr: "must be shorter than SETTLE_TIMEOUT",
		},
		{
			name: "an unparseable duration",
			env: map[string]string{
				"EVM_RPC_URLS":    "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY": "0xabc",
				"VERIFY_TIMEOUT":  "thirty seconds",
			},
			wantErr: "is not a duration",
		},
		{
			name: "a non-numeric settlement ceiling",
			env: map[string]string{
				"EVM_RPC_URLS":      "eip155:84532=https://sepolia.base.org",
				"EVM_PRIVATE_KEY":   "0xabc",
				"MAX_SETTLE_AMOUNT": "10 USDC",
			},
			wantErr: "is not a non-negative integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)

			_, err := Load()

			require.Error(t, err, "want an error containing %q", tt.wantErr)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
