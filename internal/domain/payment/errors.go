package payment

// Failure reasons the facilitator itself produces. Mechanism-level reasons
// (invalid_signature, insufficient_funds, …) are passed through from the
// payment mechanism unchanged.
const (
	ReasonRateLimited     = "rate_limit_exceeded"
	ReasonAmountTooBig    = "amount_exceeds_limit"
	ReasonAmountTooSmall  = "amount_below_minimum"
	ReasonAssetNotAllowed = "asset_not_allowed"
)

// VerificationError means the payment is not valid. It is an error rather than
// a result flag because there is nothing to act on besides the reason — and it
// keeps "the payment is bad" from being mistaken for "verification did not
// happen", which callers must handle differently.
//
// Message is sent to the caller; detail is for the log only (see WithDetail).
type VerificationError struct {
	Reason  string
	Message string
	Payer   string
	detail  string
}

func (e *VerificationError) Error() string { return describe(e.Reason, e.Message, e.detail) }

// NewVerificationError builds a verification failure.
func NewVerificationError(reason, message, payer string) *VerificationError {
	return &VerificationError{Reason: reason, Message: message, Payer: payer}
}

// WithDetail attaches text that must not reach the caller, such as the payment
// mechanism's own explanation, which can quote an RPC error and with it the
// provider URL and API key. Only Error() — that is, the log — includes it.
func (e *VerificationError) WithDetail(detail string) *VerificationError {
	e.detail = detail
	return e
}

// SettlementError means the payment could not be settled. Transaction is set
// when settlement got as far as broadcasting, so a caller can look up a
// transaction that was sent but then failed.
//
// Message is sent to the caller; detail is for the log only (see WithDetail).
type SettlementError struct {
	Reason      string
	Message     string
	Payer       string
	Network     string
	Transaction string
	detail      string
}

func (e *SettlementError) Error() string { return describe(e.Reason, e.Message, e.detail) }

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

// WithDetail attaches text that must not reach the caller; see
// VerificationError.WithDetail.
func (e *SettlementError) WithDetail(detail string) *SettlementError {
	e.detail = detail
	return e
}

// describe renders a failure for the log: reason, public message, then detail.
func describe(reason, message, detail string) string {
	s := reason
	if message != "" {
		s += ": " + message
	}
	if detail != "" {
		s += " (" + detail + ")"
	}
	return s
}
