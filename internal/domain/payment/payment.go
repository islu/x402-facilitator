// Package payment holds the facilitator's core concepts: the payment documents
// a resource server submits, the results of verifying and settling them, and
// the ports that carry out that work.
//
// Nothing here imports the x402 SDK. The protocol documents stay as raw JSON —
// only the SDK parses them in full — while this package models the small slice
// the facilitator itself reasons about.
package payment

import "math/big"

// Request is one payment presented for verification or settlement: the payer's
// signed payload and the requirements it claims to satisfy.
//
// Both documents are kept as raw JSON. Re-encoding them through Go structs
// would drop fields this service does not model (extensions, and anything a
// newer protocol version adds) and change the bytes the mechanism verifies.
type Request struct {
	Payload      []byte
	Requirements []byte
}

// Metadata is the slice of a Request the facilitator reads for itself: enough
// to apply policy, label metrics and write useful logs. It is best-effort —
// the authoritative validation happens inside the payment mechanism.
type Metadata struct {
	// Version is the x402 protocol version, 1 or 2. Zero if undetermined.
	Version int
	Scheme  string
	Network string
	// Asset is the token the requirements ask for (an ERC-20 contract or an
	// SPL mint), hex addresses lowercased. Empty if absent.
	Asset string
	// Amount is the requirement amount in the asset's atomic units, empty if
	// absent or unparseable.
	Amount *big.Int
	// Payer is the paying address, empty when the payload does not carry one in
	// a form readable without chain access (Solana payloads, notably).
	Payer string
}

// VerifyResult is the outcome of a successful verification. A payment that is
// simply invalid is reported as a VerificationError, not as a result with
// IsValid false.
type VerifyResult struct {
	IsValid    bool
	Payer      string
	Extensions map[string]any
	Extra      map[string]any
}

// SettleResult is the outcome of a successful settlement.
type SettleResult struct {
	Success     bool
	Transaction string
	Network     string
	Payer       string
	Amount      string
	Extensions  map[string]any
	Extra       map[string]any
}

// Kind is one (version, scheme, network) triple the facilitator can process.
type Kind struct {
	Version int
	Scheme  string
	Network string
	Extra   map[string]any
}

// Supported describes everything the facilitator accepts, including the
// settlement addresses per CAIP namespace so callers can check they are funded.
type Supported struct {
	Kinds      []Kind
	Extensions []string
	Signers    map[string][]string
}
