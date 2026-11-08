package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, tt := range []struct {
		path, method string
		check        func(context.Context) error
		status       int
	}{
		{"/healthz", "GET", nil, 200},
		{"/readyz", "GET", nil, 200},
		{"/healthz", "GET", func(context.Context) error { return errors.New("database failed") }, 200},
		{"/readyz", "GET", func(context.Context) error { return errors.New("database failed") }, 503},
		{"/healthz", "POST", nil, 405},
		{"/missing", "GET", nil, 404},
	} {
		t.Run(tt.method+tt.path+http.StatusText(tt.status), func(t *testing.T) {
			rw := httptest.NewRecorder()
			NewRouter(log, tt.check, 1024).ServeHTTP(rw, httptest.NewRequest(tt.method, tt.path, nil))
			if rw.Code != tt.status {
				t.Fatalf("status = %d", rw.Code)
			}
			if rw.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
		})
	}
}
