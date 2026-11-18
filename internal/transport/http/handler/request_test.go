package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func Test_duplicateName(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		wantName string
		wantDup  bool
	}{
		{name: "distinct names", doc: `{"asset":"0xa","amount":"1","extra":{"name":"USDC"}}`},
		{
			// The SDK's struct decoding takes the last spelling, so the order of
			// these members decides which asset is settled.
			name:     "a case variant at the top level",
			doc:      `{"asset":"0xa","Asset":"0xb"}`,
			wantName: "Asset", wantDup: true,
		},
		{
			name:     "an exact repeat",
			doc:      `{"amount":"1","amount":"2"}`,
			wantName: "amount", wantDup: true,
		},
		{
			name:     "a case variant nested in an array of objects",
			doc:      `{"accepted":[{"payTo":"0xa"},{"payTo":"0xb","PAYTO":"0xc"}]}`,
			wantName: "PAYTO", wantDup: true,
		},
		{
			// encoding/json folds Unicode case too: K is KELVIN SIGN.
			name:     "a Unicode case variant",
			doc:      `{"kind":"a","Kind":"b"}`,
			wantName: "Kind", wantDup: true,
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
			name, dup := duplicateName([]byte(tt.doc))

			assert.Equal(t, tt.wantDup, dup)
			assert.Equal(t, tt.wantName, name)
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
