// Command facilitator runs an x402 payment facilitator: it verifies payment
// signatures and settles them on-chain on behalf of resource servers.
//
// Configuration is entirely environment-driven; see .env.example and the README.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/islu/x402-facilitator/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "facilitator: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// A missing .env is normal in container and CI deployments, where the
	// environment is injected directly.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Startup contacts every configured RPC endpoint, so bound it: an
	// unreachable endpoint should fail the boot instead of hanging forever.
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()

	app, cleanup, err := initializeApp(startupCtx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	// Background workers stop when the process starts draining.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		app.Wallets.Watch(workerCtx, cfg.BalanceCheckInterval)
	}()

	if app.Limiter.Enabled() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			app.Limiter.RunCleanup(time.Minute, workerCtx.Done())
		}()
	}

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: app.Router,
		// Settlement waits on block inclusion, so the write deadline must clear
		// the settle timeout or gin would cut a settlement off mid-flight.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.SettleTimeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		app.Logger.Info("facilitator listening",
			"port", cfg.Port,
			"networks", cfg.Networks(),
			"rate_limit_per_minute", cfg.RateLimitPerMinute,
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case sig := <-shutdown:
		// In-flight settlements have already broadcast a transaction; draining
		// lets them report the hash back instead of leaving the resource server
		// unsure whether the payer was charged.
		app.Logger.Info("shutting down", "signal", sig.String(), "grace", cfg.ShutdownTimeout.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			app.Logger.Error("graceful shutdown failed, closing connections", "error", err)
			_ = server.Close()
		}
		stopWorkers()
		workers.Wait()
		app.Logger.Info("shutdown complete")
		return nil
	}
}
