package handler

import (
	"context"
	"net/http"
	"time"
)

type Health struct{ Check func(context.Context) error }

// Live reports whether the HTTP server is running.
// @Summary Liveness
// @Produce json
// @Success 200 {object} map[string]string
// @Router /healthz [get]
func (h Health) Live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// Ready checks infrastructure required by the selected storage mode.
// @Summary Readiness
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /readyz [get]
func (h Health) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if h.Check != nil && h.Check(ctx) != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
		return
	}
	h.Live(w, r)
}
