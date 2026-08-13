// Package http assembles the facilitator's HTTP surface.
package http

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/transport/http/handler"
	"github.com/islu/x402-facilitator/internal/transport/http/middleware"
)

// NewRouter mounts the protocol and operational routes.
//
// Routes are unauthenticated: a facilitator normally serves many resource
// servers, and the protocol carries its own proof of payment. Add an auth
// middleware here if yours is meant for a closed set of callers.
func NewRouter(h *handler.Handler, cfg *config.Config, logger *slog.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(middleware.Recovery(logger), middleware.RequestLogger(logger))
	// gin trusts every proxy by default, which would let any caller spoof its
	// client IP through X-Forwarded-For.
	_ = router.SetTrustedProxies(cfg.TrustedProxies)

	handler.RegisterPaymentRoutes(router, h)
	handler.RegisterSystemRoutes(router, h)

	return router
}

// ProvideTimeouts narrows the process config down to the per-operation budgets
// the handlers enforce.
func ProvideTimeouts(cfg *config.Config) handler.Timeouts {
	return handler.Timeouts{
		Verify: cfg.VerifyTimeout,
		Settle: cfg.SettleTimeout,
	}
}
