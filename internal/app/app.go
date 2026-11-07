package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"ozon-test-task/internal/platform/config"
	httptransport "ozon-test-task/internal/transport/http"
)

// Run owns runtime resources and blocks until cancellation or a server failure.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	resources, err := newResources(ctx, cfg)
	if err != nil {
		return err
	}
	// Keep the database available until HTTP shutdown has completed.
	defer resources.Close()

	router := httptransport.NewRouter(log, resources.Check, cfg.HTTP.MaxBodyBytes)
	server := newHTTPServer(cfg.HTTP, router, log)

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}

	log.InfoContext(ctx, "starting http server", "addr", listener.Addr().String(), "storage", cfg.Storage)
	return serveHTTP(ctx, server, listener, cfg.HTTP.ShutdownTimeout, log)
}
