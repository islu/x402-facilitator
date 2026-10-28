// Package http assembles the facilitator's HTTP surface.
package http

import (
	"log/slog"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/domain/ratelimit"
	"github.com/islu/x402-facilitator/internal/transport/http/handler"
	"github.com/islu/x402-facilitator/internal/transport/http/middleware"
)

// NewRouter mounts the protocol and public operational routes.
//
// Routes are unauthenticated: a facilitator normally serves many resource
// servers, and the protocol carries its own proof of payment. Add an auth
// middleware here if yours is meant for a closed set of callers.
func NewRouter(h *handler.Handler, cfg *config.Config, clients ratelimit.ClientLimiter, logger *slog.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(middleware.Recovery(logger), middleware.RequestLogger(logger))

	// gin trusts every proxy by default, which would let any caller spoof its
	// client IP through X-Forwarded-For — and with it dodge the IP rate limit.
	_ = router.SetTrustedProxies(cfg.TrustedProxies)

	handler.RegisterPaymentRoutes(router, h, middleware.RateLimit(clients))
	handler.RegisterSystemRoutes(router, h)

	return router
}

// NewAdminRouter mounts the routes served on ADMIN_PORT. It returns a plain
// http.Handler so it cannot be confused with the public router when wired.
func NewAdminRouter(h *handler.Handler, logger *slog.Logger) nethttp.Handler {
	router := gin.New()
	router.Use(middleware.Recovery(logger))
	handler.RegisterAdminRoutes(router, h)
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
