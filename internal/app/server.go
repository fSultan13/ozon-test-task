package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"ozon-test-task/internal/platform/config"
)

func newHTTPServer(cfg config.HTTP, handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration, log *slog.Logger) error {
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()

	var serveErr error
	serverStopped := false
	select {
	case serveErr = <-result:
		serverStopped = true
	case <-ctx.Done():
	}

	// Drain active requests even if Serve stopped because of a listener error.
	shutdownErr := shutdownHTTP(server, shutdownTimeout, log)
	if !serverStopped {
		serveErr = <-result
	}

	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if serveErr != nil {
		serveErr = fmt.Errorf("serve HTTP: %w", serveErr)
	}
	return errors.Join(serveErr, shutdownErr)
}

func shutdownHTTP(server *http.Server, timeout time.Duration, log *slog.Logger) error {
	// The signal context is already canceled; graceful shutdown needs its own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	log.Info("shutting down http server")
	if err := server.Shutdown(ctx); err != nil {
		log.Warn("http shutdown failed, closing active connections")
		shutdownErr := fmt.Errorf("shutdown HTTP: %w", err)
		if closeErr := server.Close(); closeErr != nil {
			return errors.Join(shutdownErr, fmt.Errorf("close HTTP: %w", closeErr))
		}
		return shutdownErr
	}

	log.Info("http server stopped")
	return nil
}
