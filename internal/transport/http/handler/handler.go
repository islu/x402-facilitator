// Package handler translates HTTP requests into use-case calls and use-case
// results back into responses. It holds no payment logic of its own.
package handler

import (
	"time"

	apppayment "github.com/islu/x402-facilitator/internal/app/payment"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

// Timeouts bound how long each protocol operation may take. Settlement waits on
// block inclusion, so it needs the larger budget of the two.
type Timeouts struct {
	Verify time.Duration
	Settle time.Duration
}

type Handler struct {
	payment  *apppayment.Service
	exporter telemetry.Exporter
	timeouts Timeouts
}

func NewHandler(
	payments *apppayment.Service,
	exporter telemetry.Exporter,
	timeouts Timeouts,
) *Handler {
	return &Handler{
		payment:  payments,
		exporter: exporter,
		timeouts: timeouts,
	}
}
