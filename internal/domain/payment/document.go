package payment

import (
	"cmp"
	"encoding/json"
	"math/big"
	"strings"
)

// payerPaths are the payload locations that hold the paying address, in the
// order they are tried. EVM exact uses EIP-3009 authorizations; exact and upto
// Permit2 use a permit2Authorization; some payloads carry a bare payer.
var payerPaths = [][]string{
	{"authorization", "from"},
	{"permit2Authorization", "from"},
	{"payer"},
	{"from"},
}

// ParseMetadata reads the fields the facilitator needs from a request.
//
// It is deliberately forgiving: a document this cannot read still reaches the
// payment mechanism, which owns the authoritative validation and will reject it
// with a precise reason. Failing here instead would answer a malformed document
// with a vaguer error than the mechanism would have given.
//
// The fields policy acts on are decoded the way the SDK decodes them, so the
// allowlist judges the asset and amount that will actually be settled: the
// version comes from the payload's x402Version, the requirements go into a
// typed struct through encoding/json (whose case-insensitive, last-wins field
// matching a map lookup would not reproduce), and the amount field is the one
// the version uses.
func ParseMetadata(req Request) Metadata {
	var doc payloadDoc
	_ = json.Unmarshal(req.Payload, &doc)
	var requirements requirementsDoc
	_ = json.Unmarshal(req.Requirements, &requirements)

	// The scheme-specific authorization sits under the document's "payload"
	// key in both protocol versions; the document root is only searched as a
	// fallback for callers that hand over the inner object directly.
	payload := decodeObject(req.Payload)
	inner, _ := payload["payload"].(map[string]any)

	// v2 nests the accepted requirements inside the payload; v1 keeps scheme and
	// network at the top level. The requirements document wins, since that is
	// what settlement is measured against.
	meta := Metadata{
		Version: doc.X402Version,
		Payer:   normalizeAddress(lookupPayer(inner, payload)),
		Scheme:  cmp.Or(requirements.Scheme, doc.Accepted.Scheme, doc.Scheme),
		Network: cmp.Or(requirements.Network, doc.Accepted.Network, doc.Network),
		Asset:   normalizeAddress(cmp.Or(requirements.Asset, doc.Accepted.Asset)),
	}
	amount := cmp.Or(requirements.amount(doc.X402Version), doc.Accepted.amount(doc.X402Version))
	if parsed, ok := new(big.Int).SetString(amount, 10); ok {
		meta.Amount = parsed
	}
	return meta
}

// payloadDoc is the part of a payment payload ParseMetadata reads by type.
type payloadDoc struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Accepted    requirementsDoc `json:"accepted"`
}

// requirementsDoc mirrors the SDK's PaymentRequirements (v2) and
// PaymentRequirementsV1 field names.
type requirementsDoc struct {
	Scheme            string `json:"scheme"`
	Network           string `json:"network"`
	Asset             string `json:"asset"`
	Amount            string `json:"amount"`
	MaxAmountRequired string `json:"maxAmountRequired"`
}

// amount is the field the SDK settles for version: v1 names it
// maxAmountRequired, v2 amount. Reading by version rather than by whichever
// is present keeps an unrelated field from steering the policy.
func (r requirementsDoc) amount(version int) string {
	if version == 1 {
		return r.MaxAmountRequired
	}
	return r.Amount
}

func decodeObject(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// lookupPayer searches each source in order for a known payer location.
func lookupPayer(sources ...map[string]any) string {
	for _, source := range sources {
		for _, path := range payerPaths {
			if value, ok := stringAtPath(source, path); ok {
				return value
			}
		}
	}
	return ""
}

func stringAtPath(payload map[string]any, path []string) (string, bool) {
	current := payload
	for i, key := range path {
		value, ok := current[key]
		if !ok {
			return "", false
		}
		if i == len(path)-1 {
			str, isString := value.(string)
			if !isString || str == "" {
				return "", false
			}
			return str, true
		}
		nested, isMap := value.(map[string]any)
		if !isMap {
			return "", false
		}
		current = nested
	}
	return "", false
}

// normalizeAddress lowercases hex addresses so mixed-case (EIP-55 checksummed)
// spellings of one address are treated as the same account. Non-hex identifiers
// such as Solana base58 keys are case-sensitive and left untouched.
func normalizeAddress(address string) string {
	if strings.HasPrefix(address, "0x") || strings.HasPrefix(address, "0X") {
		return strings.ToLower(address)
	}
	return address
}

// AssetKey identifies one asset on one network, in the form the settlement
// policy is keyed by.
func AssetKey(network, asset string) string {
	return network + "/" + normalizeAddress(asset)
}
