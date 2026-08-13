// Package x402 adapts the x402 Go SDK to the facilitator's payment.Processor
// port. It owns everything protocol-specific: which schemes are registered for
// which networks, and how the SDK's results and errors map onto domain types.
//
// Keeping the SDK behind this package means the use cases in internal/app —
// and their tests — never touch the protocol or a chain.
package x402

import (
	"context"
	"log/slog"

	x402sdk "github.com/x402-foundation/x402/go/v2"
	exactevm "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"
	exactevmv1 "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/v1/facilitator"
	uptoevm "github.com/x402-foundation/x402/go/v2/mechanisms/evm/upto/facilitator"
	svmmech "github.com/x402-foundation/x402/go/v2/mechanisms/svm"
	exactsvm "github.com/x402-foundation/x402/go/v2/mechanisms/svm/exact/facilitator"
	exactsvmv1 "github.com/x402-foundation/x402/go/v2/mechanisms/svm/exact/v1/facilitator"

	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/infra/chain/evm"
	"github.com/islu/x402-facilitator/internal/infra/chain/svm"
)

// sdkFacilitator is the part of the SDK facilitator this adapter uses. The SDK
// constructor returns an unexported type, so this interface is how we hold a
// reference to it.
type sdkFacilitator interface {
	Verify(ctx context.Context, payloadBytes, requirementsBytes []byte) (*x402sdk.VerifyResponse, error)
	Settle(ctx context.Context, payloadBytes, requirementsBytes []byte) (*x402sdk.SettleResponse, error)
	GetSupported() x402sdk.SupportedResponse
}

// Processor implements payment.Processor on top of the x402 SDK.
type Processor struct {
	facilitator sdkFacilitator
}

var _ payment.Processor = (*Processor)(nil)

// NewProcessor registers a payment scheme for every configured network and
// returns the processor those schemes sit behind.
//
// Schemes are registered per network rather than by wildcard so /supported
// advertises exactly the networks this instance holds a funded wallet for.
func NewProcessor(
	cfg *config.Config,
	evmSigners evm.Signers,
	svmSigners svm.Signers,
	logger *slog.Logger,
) *Processor {
	facilitator := x402sdk.Newx402Facilitator()

	for _, network := range cfg.EVMNetworks {
		signer, ok := evmSigners[network.Network]
		if !ok {
			continue
		}

		networks := []x402sdk.Network{x402sdk.Network(network.Network)}
		facilitator.Register(networks, exactevm.NewExactEvmScheme(signer, &exactevm.ExactEvmSchemeConfig{
			EIP6492AllowedFactories: cfg.EIP6492AllowedFactories,
			SimulateInSettle:        cfg.SimulateInSettle,
		}))
		facilitator.Register(networks, uptoevm.NewUptoEvmScheme(signer, &uptoevm.UptoEvmSchemeConfig{
			SimulateInSettle: cfg.SimulateInSettle,
		}))

		if network.V1Alias != "" {
			facilitator.RegisterV1([]x402sdk.Network{x402sdk.Network(network.V1Alias)},
				exactevmv1.NewExactEvmSchemeV1(signer, &exactevmv1.ExactEvmSchemeV1Config{
					EIP6492AllowedFactories: cfg.EIP6492AllowedFactories,
					SimulateInSettle:        cfg.SimulateInSettle,
				}))
		}

		logger.Info("registered EVM network",
			"network", network.Network,
			"chain_id", network.ChainID.String(),
			"address", signer.Address(),
			"v1_alias", network.V1Alias,
		)
	}

	for _, network := range cfg.SVMNetworks {
		signer, ok := svmSigners[network.Network]
		if !ok {
			continue
		}

		// One cache shared by the V1 and V2 schemes on this network: it rejects
		// a second /settle for a transaction still awaiting confirmation, which
		// is what stops a client from spending one Solana payment twice.
		cache := svmmech.NewSettlementCache()
		facilitator.Register([]x402sdk.Network{x402sdk.Network(network.Network)},
			exactsvm.NewExactSvmScheme(signer, cache))
		if network.V1Alias != "" {
			facilitator.RegisterV1([]x402sdk.Network{x402sdk.Network(network.V1Alias)},
				exactsvmv1.NewExactSvmSchemeV1(signer, cache))
		}

		logger.Info("registered SVM network",
			"network", network.Network,
			"address", signer.Address(),
			"v1_alias", network.V1Alias,
		)
	}

	return &Processor{facilitator: facilitator}
}

// Verify checks a payment signature against its requirements.
func (p *Processor) Verify(ctx context.Context, req payment.Request) (payment.VerifyResult, error) {
	result, err := p.facilitator.Verify(ctx, req.Payload, req.Requirements)
	if err != nil {
		return payment.VerifyResult{}, translateVerifyError(err)
	}

	return payment.VerifyResult{
		IsValid:    result.IsValid,
		Payer:      result.Payer,
		Extensions: result.Extensions,
		Extra:      result.Extra,
	}, nil
}

// Settle broadcasts a payment and waits for confirmation.
func (p *Processor) Settle(ctx context.Context, req payment.Request) (payment.SettleResult, error) {
	result, err := p.facilitator.Settle(ctx, req.Payload, req.Requirements)
	if err != nil {
		return payment.SettleResult{}, translateSettleError(err)
	}

	return payment.SettleResult{
		Success:     result.Success,
		Transaction: result.Transaction,
		Network:     string(result.Network),
		Payer:       result.Payer,
		Amount:      result.Amount,
		Extensions:  result.Extensions,
		Extra:       result.Extra,
	}, nil
}

// Supported lists the registered payment kinds.
func (p *Processor) Supported(context.Context) payment.Supported {
	supported := p.facilitator.GetSupported()

	kinds := make([]payment.Kind, 0, len(supported.Kinds))
	for _, kind := range supported.Kinds {
		kinds = append(kinds, payment.Kind{
			Version: kind.X402Version,
			Scheme:  kind.Scheme,
			Network: kind.Network,
			Extra:   kind.Extra,
		})
	}

	return payment.Supported{
		Kinds:      kinds,
		Extensions: supported.Extensions,
		Signers:    supported.Signers,
	}
}
