// Package config loads facilitator configuration from environment variables.
//
// Networks are addressed by their CAIP-2 identifier (e.g. "eip155:84532").
// RPC endpoints are what enable a network: every network listed in EVM_RPC_URLS
// or SVM_RPC_URLS gets a scheme registered on the facilitator.
package config

import (
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EVMNetwork is one registered EVM network and the wallet that settles on it.
type EVMNetwork struct {
	// Network is the CAIP-2 identifier, e.g. "eip155:84532".
	Network string
	// ChainID is derived from the CAIP-2 reference and checked against the RPC
	// endpoint at startup so a mismatched RPC URL fails fast instead of
	// broadcasting on the wrong chain.
	ChainID *big.Int
	RPCURL  string
	// PrivateKey is the settlement wallet for this network. Best practice is one
	// wallet per network (see EVM_PRIVATE_KEYS).
	PrivateKey string
	// V1Alias is the legacy (pre-CAIP-2) network name, e.g. "base-sepolia".
	// Empty means no V1 scheme is registered for this network.
	V1Alias string
}

// SVMNetwork is one registered Solana network and the fee payer that settles on it.
type SVMNetwork struct {
	Network    string
	RPCURL     string
	PrivateKey string
	V1Alias    string
}

// Config is the fully resolved facilitator configuration.
type Config struct {
	Port     string
	LogLevel slog.Level
	LogJSON  bool

	// TrustedProxies restricts which proxy IPs gin will trust for client IP
	// resolution. Empty disables proxy trust entirely (the safe default).
	TrustedProxies []string

	EVMNetworks []EVMNetwork
	SVMNetworks []SVMNetwork

	// EIP6492AllowedFactories lists smart-wallet factory contracts the
	// facilitator may call when deploying an undeployed ERC-6492 wallet.
	// Empty (the default) denies all factory deployment.
	EIP6492AllowedFactories []string
	// SimulateInSettle reruns transfer simulation during settle. Verify always
	// simulates; this trades an extra RPC round trip for a tighter guarantee.
	SimulateInSettle bool

	VerifyTimeout   time.Duration
	SettleTimeout   time.Duration
	ShutdownTimeout time.Duration

	// ReceiptTimeout bounds how long a settlement waits for its transaction
	// receipt. Keep it below SettleTimeout.
	ReceiptTimeout time.Duration
	// MaxFeePerGasWei caps the gas price the facilitator is willing to pay.
	// Nil means uncapped.
	MaxFeePerGasWei *big.Int
	// GasLimit is the fallback gas limit used when estimation fails.
	GasLimit uint64

	// RateLimitPerMinute caps verify+settle calls per payer address. 0 disables.
	RateLimitPerMinute int
	// MaxSettleAmount rejects settlements whose requirement amount exceeds this
	// value, in the asset's atomic units. Nil disables the check.
	MaxSettleAmount *big.Int

	// BalanceCheckInterval controls how often facilitator wallet balances are
	// polled for the gas gauge. 0 disables the poller.
	BalanceCheckInterval time.Duration
	// MinBalanceWei is the native-balance threshold that triggers a warning.
	MinBalanceWei *big.Int
}

// v1Aliases are the legacy network names for the chains that shipped before
// CAIP-2 identifiers. Operators can override or extend via EVM_V1_ALIASES /
// SVM_V1_ALIASES.
var v1Aliases = map[string]string{
	"eip155:8453":  "base",
	"eip155:84532": "base-sepolia",
	"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": "solana",
	"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": "solana-devnet",
}

// Load reads configuration from the process environment.
//
// Returns an error describing every problem found, so a misconfigured
// deployment fails at startup rather than on the first payment.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                    envString("PORT", "4022"),
		LogLevel:                parseLogLevel(envString("LOG_LEVEL", "info")),
		LogJSON:                 envBool("LOG_JSON", true),
		TrustedProxies:          splitList(os.Getenv("TRUSTED_PROXIES")),
		EIP6492AllowedFactories: splitList(os.Getenv("EIP6492_ALLOWED_FACTORIES")),
		SimulateInSettle:        envBool("SIMULATE_IN_SETTLE", false),
		GasLimit:                uint64(envInt("GAS_LIMIT", 300_000)),
	}

	var errs []string
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	var err error
	if cfg.VerifyTimeout, err = envDuration("VERIFY_TIMEOUT", 30*time.Second); err != nil {
		fail("%v", err)
	}
	if cfg.SettleTimeout, err = envDuration("SETTLE_TIMEOUT", 90*time.Second); err != nil {
		fail("%v", err)
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 30*time.Second); err != nil {
		fail("%v", err)
	}
	if cfg.ReceiptTimeout, err = envDuration("RECEIPT_TIMEOUT", 60*time.Second); err != nil {
		fail("%v", err)
	}
	if cfg.BalanceCheckInterval, err = envDuration("BALANCE_CHECK_INTERVAL", 5*time.Minute); err != nil {
		fail("%v", err)
	}

	if cfg.MaxFeePerGasWei, err = envGweiAsWei("MAX_GAS_PRICE_GWEI"); err != nil {
		fail("%v", err)
	}
	if cfg.MinBalanceWei, err = envBigInt("MIN_BALANCE_WEI"); err != nil {
		fail("%v", err)
	}
	if cfg.MinBalanceWei == nil {
		// 0.05 native units.
		cfg.MinBalanceWei = new(big.Int).Div(big.NewInt(1e18), big.NewInt(20))
	}
	if cfg.MaxSettleAmount, err = envBigInt("MAX_SETTLE_AMOUNT"); err != nil {
		fail("%v", err)
	}
	cfg.RateLimitPerMinute = envInt("RATE_LIMIT_PER_MINUTE", 0)

	evmRPCs, err := parseNetworkMap("EVM_RPC_URLS")
	if err != nil {
		fail("%v", err)
	}
	svmRPCs, err := parseNetworkMap("SVM_RPC_URLS")
	if err != nil {
		fail("%v", err)
	}
	evmKeys, err := parseNetworkMap("EVM_PRIVATE_KEYS")
	if err != nil {
		fail("%v", err)
	}
	svmKeys, err := parseNetworkMap("SVM_PRIVATE_KEYS")
	if err != nil {
		fail("%v", err)
	}
	evmAliases, err := parseNetworkMap("EVM_V1_ALIASES")
	if err != nil {
		fail("%v", err)
	}
	svmAliases, err := parseNetworkMap("SVM_V1_ALIASES")
	if err != nil {
		fail("%v", err)
	}

	enableV1 := envBool("ENABLE_V1", true)
	defaultEVMKey := strings.TrimSpace(os.Getenv("EVM_PRIVATE_KEY"))
	defaultSVMKey := strings.TrimSpace(os.Getenv("SVM_PRIVATE_KEY"))

	for _, network := range sortedKeys(evmRPCs) {
		chainID, cErr := evmChainIDFromCAIP2(network)
		if cErr != nil {
			fail("EVM_RPC_URLS: %v", cErr)
			continue
		}
		key := firstNonEmpty(evmKeys[network], defaultEVMKey)
		if key == "" {
			fail("no private key for %s: set EVM_PRIVATE_KEY or EVM_PRIVATE_KEYS[%s]", network, network)
			continue
		}
		cfg.EVMNetworks = append(cfg.EVMNetworks, EVMNetwork{
			Network:    network,
			ChainID:    chainID,
			RPCURL:     evmRPCs[network],
			PrivateKey: key,
			V1Alias:    resolveV1Alias(enableV1, network, evmAliases),
		})
	}

	for _, network := range sortedKeys(svmRPCs) {
		if !strings.HasPrefix(network, "solana:") {
			fail("SVM_RPC_URLS: %q is not a solana CAIP-2 network", network)
			continue
		}
		key := firstNonEmpty(svmKeys[network], defaultSVMKey)
		if key == "" {
			fail("no private key for %s: set SVM_PRIVATE_KEY or SVM_PRIVATE_KEYS[%s]", network, network)
			continue
		}
		cfg.SVMNetworks = append(cfg.SVMNetworks, SVMNetwork{
			Network:    network,
			RPCURL:     svmRPCs[network],
			PrivateKey: key,
			V1Alias:    resolveV1Alias(enableV1, network, svmAliases),
		})
	}

	if len(cfg.EVMNetworks) == 0 && len(cfg.SVMNetworks) == 0 && len(errs) == 0 {
		fail("no networks configured: set EVM_RPC_URLS and/or SVM_RPC_URLS")
	}
	if cfg.ReceiptTimeout >= cfg.SettleTimeout {
		fail("RECEIPT_TIMEOUT (%s) must be shorter than SETTLE_TIMEOUT (%s)", cfg.ReceiptTimeout, cfg.SettleTimeout)
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return cfg, nil
}

// Networks lists every registered network, for logging and readiness output.
func (c *Config) Networks() []string {
	networks := make([]string, 0, len(c.EVMNetworks)+len(c.SVMNetworks))
	for _, n := range c.EVMNetworks {
		networks = append(networks, n.Network)
	}
	for _, n := range c.SVMNetworks {
		networks = append(networks, n.Network)
	}
	return networks
}

func resolveV1Alias(enabled bool, network string, overrides map[string]string) string {
	if !enabled {
		return ""
	}
	if alias, ok := overrides[network]; ok {
		return alias
	}
	return v1Aliases[network]
}

// evmChainIDFromCAIP2 extracts the chain ID from an "eip155:<chainID>" network.
func evmChainIDFromCAIP2(network string) (*big.Int, error) {
	namespace, reference, ok := strings.Cut(network, ":")
	if !ok || namespace != "eip155" {
		return nil, fmt.Errorf("%q is not an eip155 CAIP-2 network", network)
	}
	chainID, ok := new(big.Int).SetString(reference, 10)
	if !ok || chainID.Sign() <= 0 {
		return nil, fmt.Errorf("%q has an invalid chain ID reference", network)
	}
	return chainID, nil
}

// parseNetworkMap parses a "network=value,network=value" environment variable.
// Values may contain "=" (RPC URLs with query strings), so only the first
// separator is significant.
func parseNetworkMap(name string) (map[string]string, error) {
	out := map[string]string{}
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		network, value, ok := strings.Cut(entry, "=")
		network = strings.TrimSpace(network)
		value = strings.TrimSpace(value)
		if !ok || network == "" || value == "" {
			return nil, fmt.Errorf("%s: %q is not in network=value form", name, entry)
		}
		if _, duplicate := out[network]; duplicate {
			return nil, fmt.Errorf("%s: duplicate entry for %q", name, network)
		}
		out[network] = value
	}
	return out, nil
}

// sortedKeys returns map keys in a stable order, so /supported output and
// startup logs do not reshuffle between runs.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func envString(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (e.g. 30s, 2m)", name, raw)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s: must be positive, got %q", name, raw)
	}
	return v, nil
}

func envBigInt(name string) (*big.Int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("%s: %q is not a non-negative integer", name, raw)
	}
	return v, nil
}

func envGweiAsWei(name string) (*big.Int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	gwei, ok := new(big.Int).SetString(raw, 10)
	if !ok || gwei.Sign() <= 0 {
		return nil, fmt.Errorf("%s: %q is not a positive integer (gwei)", name, raw)
	}
	return gwei.Mul(gwei, big.NewInt(1e9)), nil
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
