package payment

import (
	"fmt"
	"math/big"
)

// Policy is the facilitator's own limits on what it will process, independent
// of whether a payment is cryptographically valid.
type Policy struct {
	// Assets is the allowlist of what this facilitator settles, keyed by
	// AssetKey. Empty allows any asset.
	//
	// The allowlist is a gas guard, not a business rule: without it anyone can
	// deploy a worthless EIP-3009 token and have the settlement wallet pay gas
	// to move it.
	Assets map[string]AssetLimit
}

// AssetLimit bounds the amount of one asset a single settlement may move, in
// the asset's atomic units. A nil bound is open.
type AssetLimit struct {
	// Min keeps out payments worth less than the gas spent settling them.
	Min *big.Int
	// Max caps what a single compromised or buggy caller can move through the
	// settlement wallet.
	Max *big.Int
}

// Check reports why a payment falls outside the policy, as a failure reason
// and message, or an empty reason when it is allowed.
//
// An unreadable asset is rejected whenever an allowlist is configured, since
// the allowlist is only a guard if it fails closed. An unreadable amount
// passes: the mechanism rejects documents whose amount is malformed, and
// failing closed here would reject schemes whose amount is expressed
// differently.
func (p Policy) Check(meta Metadata) (reason, message string) {
	if len(p.Assets) == 0 {
		return "", ""
	}
	limit, ok := p.Assets[AssetKey(meta.Network, meta.Asset)]
	if !ok {
		return ReasonAssetNotAllowed, fmt.Sprintf("asset %q on %s is not accepted by this facilitator", meta.Asset, meta.Network)
	}
	if meta.Amount == nil {
		return "", ""
	}
	if limit.Max != nil && meta.Amount.Cmp(limit.Max) > 0 {
		return ReasonAmountTooBig, "payment amount exceeds this facilitator's per-settlement limit"
	}
	if limit.Min != nil && meta.Amount.Cmp(limit.Min) < 0 {
		return ReasonAmountTooSmall, "payment amount is below this facilitator's per-settlement minimum"
	}
	return "", ""
}
