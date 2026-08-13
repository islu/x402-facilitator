package payment

import "context"

//go:generate go tool mockgen -destination=../../mocks/domain/processor.go -package=mocks -source=processor.go

// Processor verifies and settles payments against a blockchain.
//
// It is the port the x402 payment mechanisms sit behind: the implementation in
// internal/infra/x402 owns the protocol and the chain, while use cases in
// internal/app depend only on this. Failures arrive as *VerificationError or
// *SettlementError when the payment itself is at fault, and as ordinary errors
// when the facilitator could not reach a verdict.
type Processor interface {
	// Verify checks the payment authorizes what the requirements ask for,
	// without moving funds.
	Verify(ctx context.Context, req Request) (VerifyResult, error)
	// Settle broadcasts the payment and waits for it to be confirmed.
	Settle(ctx context.Context, req Request) (SettleResult, error)
	// Supported lists the payment kinds this processor can handle.
	Supported(ctx context.Context) Supported
}
