package payment

import "fmt"

// Failure reasons the facilitator itself produces. Mechanism-level reasons
// (invalid_signature, insufficient_funds, …) are passed through from the
// payment mechanism unchanged.
const (
	ReasonRateLimited  = "rate_limit_exceeded"
	ReasonAmountTooBig = "amount_exceeds_limit"
)

// VerificationError means the payment is not valid. It is an error rather than
// a result flag because there is nothing to act on besides the reason — and it
// keeps "the payment is bad" from being mistaken for "verification did not
// happen", which callers must handle differently.
type VerificationError struct {
	Reason  string
	Message string
	Payer   string
}

func (e *VerificationError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Reason, e.Message)
	}
	return e.Reason
}

// NewVerificationError builds a verification failure.
func NewVerificationError(reason, message, payer string) *VerificationError {
	return &VerificationError{Reason: reason, Message: message, Payer: payer}
}

// SettlementError means the payment could not be settled. Transaction is set
// when settlement got as far as broadcasting, so a caller can look up a
// transaction that was sent but then failed.
type SettlementError struct {
	Reason      string
	Message     string
	Payer       string
	Network     string
	Transaction string
}

func (e *SettlementError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Reason, e.Message)
	}
	return e.Reason
}

// NewSettlementError builds a settlement failure.
func NewSettlementError(reason, message, payer, network, transaction string) *SettlementError {
	return &SettlementError{
		Reason:      reason,
		Message:     message,
		Payer:       payer,
		Network:     network,
		Transaction: transaction,
	}
}
