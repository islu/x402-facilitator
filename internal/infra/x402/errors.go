package x402

import (
	"errors"

	x402sdk "github.com/x402-foundation/x402/go/v2"

	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// The translate functions keep the SDK's failure messages away from the
// caller: they go into the domain error's log-only detail, never its Message.
// The SDK copies raw errors into those messages, and an RPC error names the
// endpoint URL and with it the provider's API key. The reason code, which is
// the protocol's machine-readable verdict, passes through unchanged.

// translateVerifyError converts an SDK verification failure into the domain
// error. Anything the SDK did not classify as a payment verdict — an RPC
// outage, a timeout, a panic recovered upstream — is passed through unchanged,
// so the caller can tell "this payment is bad" from "we could not tell".
func translateVerifyError(err error) error {
	var verifyErr *x402sdk.VerifyError
	if !errors.As(err, &verifyErr) {
		return err
	}
	return payment.NewVerificationError(verifyErr.InvalidReason, "", verifyErr.Payer).
		WithDetail(verifyErr.InvalidMessage)
}

// translateSettleError converts an SDK settlement failure into the domain
// error, preserving the transaction hash when one was broadcast.
func translateSettleError(err error) error {
	var settleErr *x402sdk.SettleError
	if !errors.As(err, &settleErr) {
		return err
	}
	return payment.NewSettlementError(
		settleErr.ErrorReason,
		"",
		settleErr.Payer,
		string(settleErr.Network),
		settleErr.Transaction,
	).WithDetail(settleErr.ErrorMessage)
}
