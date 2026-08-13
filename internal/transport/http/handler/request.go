package handler

import (
	"encoding/json"
	"net/http"

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

// bindPayment parses and validates a payment request body, answering 400
// itself when the body is unusable.
func bindPayment(c *gin.Context) (payment.Request, bool) {
	var req PaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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

	return payment.Request{
		Payload:      req.PaymentPayload,
		Requirements: req.PaymentRequirements,
	}, true
}
