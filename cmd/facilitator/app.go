package main

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	appwallet "github.com/islu/x402-facilitator/internal/app/wallet"
)

// App is what the composition root hands back to main: the public and admin
// HTTP handlers plus the balance watcher the process itself has to drive.
type App struct {
	Router  *gin.Engine
	Admin   http.Handler
	Wallets *appwallet.Service
	Logger  *slog.Logger
}

// NewApp is the wire target that collects the assembled dependencies.
func NewApp(
	router *gin.Engine,
	admin http.Handler,
	wallets *appwallet.Service,
	logger *slog.Logger,
) *App {
	return &App{
		Router:  router,
		Admin:   admin,
		Wallets: wallets,
		Logger:  logger,
	}
}
