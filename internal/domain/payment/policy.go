package payment

import "math/big"

// Policy is the facilitator's own limits on what it will process, independent
// of whether a payment is cryptographically valid.
type Policy struct {
	// MaxSettleAmount rejects settlements above this amount, in the asset's
	// atomic units. Nil means no ceiling.
	//
	// This is a blast-radius limit, not a business rule: it caps what a single
	// compromised or buggy caller can move through the settlement wallet.
	MaxSettleAmount *big.Int
}

// CheckSettleAmount reports whether a settlement is within the configured
// ceiling.
//
// An unreadable amount passes: the ceiling is a safety net over amounts the
// facilitator understands, and the mechanism rejects documents whose amount is
// malformed. Failing closed here would reject valid payments in schemes whose
// amount is expressed differently.
func (p Policy) CheckSettleAmount(meta Metadata) error {
	if p.MaxSettleAmount == nil || meta.Amount == nil {
		return nil
	}
	if meta.Amount.Cmp(p.MaxSettleAmount) <= 0 {
		return nil
	}
	return NewSettlementError(
		ReasonAmountTooBig,
		"payment amount exceeds this facilitator's per-settlement limit",
		meta.Payer,
		meta.Network,
		"",
	)
}
