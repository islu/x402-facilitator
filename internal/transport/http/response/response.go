// Package response defines the response shapes the transport layer writes.
//
// Two conventions live here, and the split is deliberate. The x402 endpoints
// answer with the protocol's own bodies, verbatim, because resource servers
// parse them by specification — a facilitator that wraps them in a house
// envelope is not an x402 facilitator. Everything else (health, readiness,
// request errors) uses the Error envelope below.
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response is the envelope for this service's own endpoints. Data carries the
// payload; Metadata carries anything about the response itself.
type Response struct {
	Data     any      `json:"data,omitempty"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Error *ErrorInfo `json:"error,omitempty"`
}

type ErrorInfo struct {
	Message string `json:"message"`
}

// OK writes a successful non-protocol response.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Data: data})
}

// Error writes a failure the caller must fix or retry: a malformed request, or
// a facilitator that could not reach a verdict. A payment that is merely
// invalid is not this — that is a protocol answer, written by the x402
// handlers with 200 and the protocol's own body.
func Error(c *gin.Context, status int, message string) {
	c.JSON(status, Response{
		Metadata: Metadata{Error: &ErrorInfo{Message: message}},
	})
}

// Protocol writes an x402 protocol body verbatim, with no envelope.
func Protocol(c *gin.Context, body any) {
	c.JSON(http.StatusOK, body)
}
