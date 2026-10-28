// Package infra holds the adapters that connect the facilitator to the outside
// world — chains, the x402 protocol SDK, metric storage, logging — and the wire
// set that provides them.
package infra

import (
	"time"

	"github.com/google/wire"

	"github.com/islu/x402-facilitator/internal/config"
	domainpayment "github.com/islu/x402-facilitator/internal/domain/payment"
	domainratelimit "github.com/islu/x402-facilitator/internal/domain/ratelimit"
	domaintelemetry "github.com/islu/x402-facilitator/internal/domain/telemetry"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
	"github.com/islu/x402-facilitator/internal/infra/chain/evm"
	"github.com/islu/x402-facilitator/internal/infra/chain/svm"
	"github.com/islu/x402-facilitator/internal/infra/logger"
	"github.com/islu/x402-facilitator/internal/infra/ratelimit"
	"github.com/islu/x402-facilitator/internal/infra/telemetry"
	"github.com/islu/x402-facilitator/internal/infra/x402"
)

// wire injector
var ProviderInfraSet = wire.NewSet(
	// chain access: one settlement wallet per configured network
	evm.ProvideSigners,
	svm.ProvideSigners,
	ProvideWalletRegistry,

	// x402 protocol: scheme registration and the payment processor port
	x402.NewProcessor,
	wire.Bind(new(domainpayment.Processor), new(*x402.Processor)),

	// policy: one quota per payer, one per caller IP
	ProvideRateLimiter,
	ProvideClientLimiter,

	// telemetry: one registry serves both the write and read ports
	telemetry.New,
	wire.Bind(new(domaintelemetry.Recorder), new(*telemetry.Registry)),
	wire.Bind(new(domaintelemetry.Exporter), new(*telemetry.Registry)),

	// logging
	logger.ProvideLogger,
)

// ProvideRateLimiter builds the per-payer limiter. The window is a minute
// because that is the unit the configured limit is expressed in.
func ProvideRateLimiter(cfg *config.Config) domainratelimit.Limiter {
	return ratelimit.New(cfg.RateLimitPerMinute, time.Minute)
}

// ProvideClientLimiter builds the per-caller-IP limiter for the HTTP layer.
func ProvideClientLimiter(cfg *config.Config) domainratelimit.ClientLimiter {
	return ratelimit.New(cfg.RateLimitPerIPPerMinute, time.Minute)
}

// ProvideWalletRegistry collects every chain's settlement wallets into the
// registry the wallet use cases read balances from.
func ProvideWalletRegistry(evmSigners evm.Signers, svmSigners svm.Signers) wallet.Registry {
	registry := wallet.Registry{}
	for network, reader := range evmSigners.Wallets() {
		registry[network] = reader
	}
	for network, reader := range svmSigners.Wallets() {
		registry[network] = reader
	}
	return registry
}
