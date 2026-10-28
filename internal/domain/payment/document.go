package payment

import (
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
func ParseMetadata(req Request) Metadata {
	payload := decodeObject(req.Payload)
	requirements := decodeObject(req.Requirements)

	// The scheme-specific authorization sits under the document's "payload"
	// key in both protocol versions; the document root is only searched as a
	// fallback for callers that hand over the inner object directly.
	inner, _ := payload["payload"].(map[string]any)

	meta := Metadata{
		Payer: normalizeAddress(lookupPayer(inner, payload)),
	}

	if version, ok := numberAt(payload, "x402Version"); ok {
		meta.Version = version
	}

	// v2 nests the accepted requirements inside the payload; v1 keeps scheme and
	// network at the top level. The requirements document is preferred when both
	// carry the field, since that is what settlement is measured against.
	accepted, _ := payload["accepted"].(map[string]any)
	meta.Scheme = firstString(requirements, accepted, payload)("scheme")
	meta.Network = firstString(requirements, accepted, payload)("network")
	meta.Asset = normalizeAddress(firstString(requirements, accepted)("asset"))

	// v1 calls it maxAmountRequired, v2 calls it amount.
	amount := firstString(requirements, accepted)("amount")
	if amount == "" {
		amount = firstString(requirements, accepted)("maxAmountRequired")
	}
	if parsed, ok := new(big.Int).SetString(amount, 10); ok {
		meta.Amount = parsed
	}

	return meta
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

// firstString returns a lookup that reads a key from the first source that has
// it as a non-empty string.
func firstString(sources ...map[string]any) func(key string) string {
	return func(key string) string {
		for _, source := range sources {
			if value, ok := source[key].(string); ok && value != "" {
				return value
			}
		}
		return ""
	}
}

func numberAt(source map[string]any, key string) (int, bool) {
	// JSON numbers decode to float64 through map[string]any.
	value, ok := source[key].(float64)
	if !ok {
		return 0, false
	}
	return int(value), true
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
