package http

import (
	"context"
	"net/http"

	"ozon-test-task/internal/transport/http/handler"
)

// registerHealthRoutes registers the liveness and readiness endpoints.
func registerHealthRoutes(mux *http.ServeMux, check func(context.Context) error) {
	health := handler.Health{Check: check}
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)
}
