package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/transport/http/response"
)

// The x402 wire bodies. These field names and shapes are the protocol's, not
// this service's — resource servers parse them by specification, so they are
// written verbatim rather than through the response envelope.

type VerifyResponse struct {
	IsValid        bool           `json:"isValid"`
	InvalidReason  string         `json:"invalidReason,omitempty"`
	InvalidMessage string         `json:"invalidMessage,omitempty"`
	Payer          string         `json:"payer,omitempty"`
	Extensions     map[string]any `json:"extensions,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

type SettleResponse struct {
	Success      bool           `json:"success"`
	ErrorReason  string         `json:"errorReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Payer        string         `json:"payer,omitempty"`
	Transaction  string         `json:"transaction"`
	Network      string         `json:"network"`
	Amount       string         `json:"amount,omitempty"`
	Extensions   map[string]any `json:"extensions,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type SupportedResponse struct {
	Kinds      []SupportedKind     `json:"kinds"`
	Extensions []string            `json:"extensions"`
	Signers    map[string][]string `json:"signers,omitempty"`
}

type SupportedKind struct {
	X402Version int            `json:"x402Version"`
	Scheme      string         `json:"scheme"`
	Network     string         `json:"network"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// RegisterPaymentRoutes mounts the three endpoints the x402 protocol defines.
func RegisterPaymentRoutes(router gin.IRoutes, h *Handler) {
	router.POST("/verify", h.Verify)
	router.POST("/settle", h.Settle)
	router.GET("/supported", h.Supported)
}

// Verify checks a payment signature without moving funds.
//
// A payment that is simply invalid is a successful HTTP call answering "no":
// 200 with isValid false and a machine-readable reason. 5xx is reserved for the
// facilitator failing to reach a verdict at all, which is what tells a resource
// server to retry rather than reject the buyer.
func (h *Handler) Verify(c *gin.Context) {
	req, ok := bindPayment(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeouts.Verify)
	defer cancel()

	result, err := h.payment.VerifyPayment(ctx, req)
	if err != nil {
		var invalid *payment.VerificationError
		if errors.As(err, &invalid) {
			response.Protocol(c, VerifyResponse{
				IsValid:        false,
				InvalidReason:  invalid.Reason,
				InvalidMessage: invalid.Message,
				Payer:          invalid.Payer,
			})
			return
		}
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Protocol(c, VerifyResponse{
		IsValid:    result.IsValid,
		Payer:      result.Payer,
		Extensions: result.Extensions,
		Extra:      result.Extra,
	})
}

// Settle broadcasts the payment and waits for confirmation.
//
// Failures follow verify's convention: 200 with success false, plus the
// transaction hash when one was broadcast, since a resource server needs both
// to decide what to do about a payment that may have moved funds anyway.
func (h *Handler) Settle(c *gin.Context) {
	req, ok := bindPayment(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeouts.Settle)
	defer cancel()

	result, err := h.payment.SettlePayment(ctx, req)
	if err != nil {
		var failed *payment.SettlementError
		if errors.As(err, &failed) {
			response.Protocol(c, SettleResponse{
				Success:      false,
				ErrorReason:  failed.Reason,
				ErrorMessage: failed.Message,
				Payer:        failed.Payer,
				Transaction:  failed.Transaction,
				Network:      failed.Network,
			})
			return
		}
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Protocol(c, SettleResponse{
		Success:     result.Success,
		Payer:       result.Payer,
		Transaction: result.Transaction,
		Network:     result.Network,
		Amount:      result.Amount,
		Extensions:  result.Extensions,
		Extra:       result.Extra,
	})
}

// Supported lists the payment kinds this facilitator can process.
func (h *Handler) Supported(c *gin.Context) {
	supported := h.payment.ListSupported(c.Request.Context())

	kinds := make([]SupportedKind, 0, len(supported.Kinds))
	for _, kind := range supported.Kinds {
		kinds = append(kinds, SupportedKind{
			X402Version: kind.Version,
			Scheme:      kind.Scheme,
			Network:     kind.Network,
			Extra:       kind.Extra,
		})
	}

	response.Protocol(c, SupportedResponse{
		Kinds:      kinds,
		Extensions: orEmpty(supported.Extensions),
		Signers:    supported.Signers,
	})
}

// orEmpty keeps a nil slice from serializing as null: the protocol expects an
// array for extensions.
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
