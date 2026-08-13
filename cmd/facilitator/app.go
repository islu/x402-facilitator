package main

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	appwallet "github.com/islu/x402-facilitator/internal/app/wallet"
	"github.com/islu/x402-facilitator/internal/infra/ratelimit"
)

// App is what the composition root hands back to main: the HTTP handler plus
// the pieces the process itself has to drive — the balance watcher and the rate
// limiter's cleanup loop.
type App struct {
	Router  *gin.Engine
	Wallets *appwallet.Service
	Limiter *ratelimit.SlidingWindow
	Logger  *slog.Logger
}

// NewApp is the wire target that collects the assembled dependencies.
func NewApp(
	router *gin.Engine,
	wallets *appwallet.Service,
	limiter *ratelimit.SlidingWindow,
	logger *slog.Logger,
) *App {
	return &App{
		Router:  router,
		Wallets: wallets,
		Limiter: limiter,
		Logger:  logger,
	}
}
