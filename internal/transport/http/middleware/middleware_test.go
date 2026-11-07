package middleware

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) != w.Header().Get("X-Request-ID") {
			t.Fatal("request IDs differ")
		}
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})
	r := httptest.NewRequest("GET", "/secret?token=secret", nil)
	r.Header.Set("X-Request-ID", "untrusted")
	w := httptest.NewRecorder()
	Wrap(next, log, 4).ServeHTTP(w, r)
	if w.Code != 201 || len(w.Header().Get("X-Request-ID")) != 32 {
		t.Fatalf("response = %v", w)
	}
	if !strings.Contains(logs.String(), `"status":201`) || strings.Contains(logs.String(), "secret") {
		t.Fatalf("unexpected logs: %s", logs.String())
	}
}

func TestRecovery(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	w := httptest.NewRecorder()
	Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("sensitive failure") }), log, 1024).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "sensitive") {
		t.Fatalf("response = %v", w)
	}
}

func TestRecoveryAfterWriteAborts(t *testing.T) {
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Fatalf("panic = %v", v)
		}
	}()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("started")); panic("failed") }), log, 1024).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestBodyLimit(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var limitErr *http.MaxBytesError
		if !errors.As(err, &limitErr) {
			t.Fatalf("body error = %v", err)
		}
	}), log, 4).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader("12345")))
}

func BenchmarkWrap(b *testing.B) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), log, 1024)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		r := httptest.NewRequest("GET", "/healthz", nil)
		for pb.Next() {
			h.ServeHTTP(httptest.NewRecorder(), r)
		}
	})
}
