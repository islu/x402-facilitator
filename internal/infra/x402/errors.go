package x402

import (
	"errors"

	x402sdk "github.com/x402-foundation/x402/go/v2"

	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// translateVerifyError converts an SDK verification failure into the domain
// error. Anything the SDK did not classify as a payment verdict — an RPC
// outage, a timeout, a panic recovered upstream — is passed through unchanged,
// so the caller can tell "this payment is bad" from "we could not tell".
func translateVerifyError(err error) error {
	var verifyErr *x402sdk.VerifyError
	if !errors.As(err, &verifyErr) {
		return err
	}
	return payment.NewVerificationError(verifyErr.InvalidReason, verifyErr.InvalidMessage, verifyErr.Payer)
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
		settleErr.ErrorMessage,
		settleErr.Payer,
		string(settleErr.Network),
		settleErr.Transaction,
	)
}
