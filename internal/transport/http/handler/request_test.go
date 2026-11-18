package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func Test_checkMemberNames(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		// wantErr is the refusal, or empty for a document that passes.
		wantErr string
	}{
		{name: "distinct names", doc: `{"asset":"0xa","amount":"1","extra":{"name":"USDC"}}`},
		{
			// The SDK's struct decoding takes the last spelling, so the order of
			// these members decides which asset is settled.
			name:    "a case variant at the top level",
			doc:     `{"asset":"0xa","Asset":"0xb"}`,
			wantErr: `repeats the member name "Asset"`,
		},
		{
			name:    "an exact repeat",
			doc:     `{"amount":"1","amount":"2"}`,
			wantErr: `repeats the member name "amount"`,
		},
		{
			name:    "a case variant nested in an array of objects",
			doc:     `{"accepted":[{"payTo":"0xa"},{"payTo":"0xb","PAYTO":"0xc"}]}`,
			wantErr: `repeats the member name "PAYTO"`,
		},
		{
			// encoding/json folds Unicode case too: K is KELVIN SIGN.
			name:    "a Unicode case variant",
			doc:     `{"kind":"a","Kind":"b"}`,
			wantErr: `repeats the member name "Kind"`,
		},
		{
			// 1e1000 is valid JSON but overflows a float64; scanning must not
			// stop there and miss the repeat after it.
			name:    "a repeat after a number too large for a float64",
			doc:     `{"padding":1e1000,"amount":"2","Amount":"1"}`,
			wantErr: `repeats the member name "Amount"`,
		},
		{
			name:    "the same repeat in the other order",
			doc:     `{"padding":1e1000,"Amount":"1","amount":"2"}`,
			wantErr: `repeats the member name "amount"`,
		},
		{
			// Whatever stops the scan, the document is refused, not passed.
			name:    "a document the scan cannot finish",
			doc:     `{"amount":"1","asset":`,
			wantErr: "cannot be read: it ends inside an object or array",
		},
		{
			name: "one name in separate objects",
			doc:  `{"payload":{"from":"0xa"},"authorization":{"from":"0xb"}}`,
		},
		{
			name: "a value that spells an earlier name",
			doc:  `{"asset":"asset","list":["asset","Asset"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMemberNames([]byte(tt.doc))

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

// An ambiguous document is refused before it reaches the use case, where the
// settlement log would key it.
func Test_AmbiguousDocumentRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	router, _ := newTestRouter(ctrl) // the processor expects no call
	body := `{
  "x402Version": 2,
  "paymentPayload": {"x402Version": 2, "payload": {"authorization": {"from": "0xabc"}}},
  "paymentRequirements": {"scheme": "exact", "network": "eip155:84532", "asset": "0xb", "Asset": "0xa", "amount": "1000"}
}`

	for _, path := range []string{"/verify", "/settle"} {
		rec := post(t, router, path, body)

		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
		assert.Contains(t, rec.Body.String(), `paymentRequirements repeats the member name \"Asset\"`, path)
	}
}
