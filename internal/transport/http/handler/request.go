package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/transport/http/response"
)

// PaymentRequest is the body both /verify and /settle accept.
//
// The two payment documents stay as raw JSON: the SDK detects the protocol
// version from them, and re-encoding through Go structs would drop fields this
// service does not model.
type PaymentRequest struct {
	X402Version         int             `json:"x402Version"`
	PaymentPayload      json.RawMessage `json:"paymentPayload"`
	PaymentRequirements json.RawMessage `json:"paymentRequirements"`
}

// maxBodyBytes bounds a payment request body. Real payloads are a few KB (a
// Solana transaction is capped at 1232 bytes); the body is decoded twice, so an
// unbounded one is a cheap way to exhaust memory.
const maxBodyBytes = 64 << 10

// bindPayment parses and validates a payment request body, answering 4xx
// itself when the body is unusable.
func bindPayment(c *gin.Context) (payment.Request, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)

	var req PaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			response.Error(c, http.StatusRequestEntityTooLarge, "request body exceeds 64 KiB")
			return payment.Request{}, false
		}
		response.Error(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return payment.Request{}, false
	}
	if len(req.PaymentPayload) == 0 {
		response.Error(c, http.StatusBadRequest, "paymentPayload is required")
		return payment.Request{}, false
	}
	if len(req.PaymentRequirements) == 0 {
		response.Error(c, http.StatusBadRequest, "paymentRequirements is required")
		return payment.Request{}, false
	}
	for field, doc := range map[string][]byte{
		"paymentPayload":      req.PaymentPayload,
		"paymentRequirements": req.PaymentRequirements,
	} {
		if err := checkMemberNames(doc); err != nil {
			response.Error(c, http.StatusBadRequest, field+" "+err.Error())
			return payment.Request{}, false
		}
	}

	return payment.Request{
		Payload:      req.PaymentPayload,
		Requirements: req.PaymentRequirements,
	}, true
}

// checkMemberNames refuses doc if a member name repeats within one of its
// objects, compared the way encoding/json matches a member to a struct field:
// case insensitively.
//
// The SDK decodes the documents into structs, where {"asset":"A","Asset":"B"}
// silently resolves to its last spelling, so the same members in another order
// are another payment. Anything that reads the document by other rules (the
// settlement key sorts member names) would then take two payments for one.
// No client means anything by such a document, so it is refused here, once,
// for every reader downstream. A document the scan cannot finish is refused
// too, rather than passed unchecked.
func checkMemberNames(doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	// Numbers stay text: a valid 1e1000 overflows a float64 and would end the
	// scan before the members after it.
	dec.UseNumber()
	// One entry per open container: an object's member names so far, or nil
	// for an array.
	var open []map[string]bool
	expectName := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if len(open) > 0 { // Token reports a document cut off inside a container as a plain EOF
				return errors.New("cannot be read: it ends inside an object or array")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot be read: %w", err)
		}
		if name, ok := tok.(string); ok && expectName {
			folded := foldName(name)
			if open[len(open)-1][folded] {
				return fmt.Errorf("repeats the member name %q", name)
			}
			open[len(open)-1][folded] = true
			expectName = false
			continue
		}
		switch tok {
		case json.Delim('{'):
			open = append(open, map[string]bool{})
			expectName = true
			continue
		case json.Delim('['):
			open = append(open, nil)
			continue
		case json.Delim('}'), json.Delim(']'):
			open = open[:len(open)-1]
		}
		// A value just ended, a scalar or a closed container: inside an object,
		// a member name or '}' comes next.
		expectName = len(open) > 0 && open[len(open)-1] != nil
	}
}

// foldName maps every case variant of name to one key: each rune becomes the
// smallest rune of its Unicode simple-folding orbit, so foldName(a) ==
// foldName(b) exactly when strings.EqualFold(a, b), the rule encoding/json uses.
func foldName(name string) string {
	return strings.Map(func(r rune) rune {
		smallest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			smallest = min(smallest, f)
		}
		return smallest
	}, name)
}
