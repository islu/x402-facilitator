// Package logger builds the process logger from configuration.
package logger

import (
	"log/slog"
	"os"

	"github.com/islu/x402-facilitator/internal/config"
)

// ProvideLogger returns the application logger and installs it as the slog
// default, so any library reaching for slog.Default lands in the same stream.
//
// JSON is the default because these logs are read by a collector in every
// environment that matters; text is for a terminal.
func ProvideLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	var handler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if cfg.LogJSON {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
