package http

import "net/http"

// RegisterRoutes connects the shortener endpoints to the shared HTTP mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /links", h.Shorten)
	mux.HandleFunc("GET /links/{code}", h.Resolve)
}
