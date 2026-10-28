//go:build wireinject

package main

import (
	"context"

	"github.com/google/wire"

	"github.com/islu/x402-facilitator/internal/app"
	"github.com/islu/x402-facilitator/internal/config"
	"github.com/islu/x402-facilitator/internal/infra"
	httpTransport "github.com/islu/x402-facilitator/internal/transport/http"
	"github.com/islu/x402-facilitator/internal/transport/http/handler"
)

// initializeApp is the composition root: wire generates the body in
// wire_gen.go. Run `make wire` (or `wire gen ./cmd/facilitator/`) after
// changing a provider.
func initializeApp(ctx context.Context, cfg *config.Config) (*App, func(), error) {
	wire.Build(
		infra.ProviderInfraSet,
		app.ProviderAppSet,
		httpTransport.ProvideTimeouts,
		handler.NewHandler,
		httpTransport.NewRouter,
		httpTransport.NewAdminRouter,
		NewApp,
	)
	return nil, nil, nil
}
