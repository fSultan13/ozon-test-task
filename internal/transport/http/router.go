package http

import (
	"context"
	"log/slog"
	"net/http"

	"ozon-test-task/internal/transport/http/middleware"
)

func NewRouter(log *slog.Logger, check func(context.Context) error, maxBodyBytes int64, registerRoutes ...func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	registerHealthRoutes(mux, check)
	for _, register := range registerRoutes {
		register(mux)
	}
	return middleware.Wrap(mux, log, maxBodyBytes)
}
