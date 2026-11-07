package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type requestIDKey struct{}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *recorder) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	if code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

// Wrap assigns a server-generated ID, logs requests, recovers panics and limits bodies.
func Wrap(next http.Handler, log *slog.Logger, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			http.Error(w, "internal server error", 500)
			return
		}
		requestID := hex.EncodeToString(id[:])
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		rw := &recorder{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if value := recover(); value != nil {
				if value == http.ErrAbortHandler {
					panic(value)
				}
				log.ErrorContext(r.Context(), "request panic", "request_id", requestID, "stack", string(debug.Stack()))
				if rw.status != 0 {
					panic(http.ErrAbortHandler)
				}
				http.Error(rw, "internal server error", http.StatusInternalServerError)
			}
			status := rw.status
			if status == 0 {
				status = http.StatusOK
			}
			// URLs and query strings may contain sensitive user data.
			log.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("request_id", requestID),
				slog.String("method", r.Method),
				slog.Int("status", status),
				slog.Int("bytes", rw.bytes),
				slog.Duration("duration", time.Since(started)),
			)
		}()
		next.ServeHTTP(rw, r)
	})
}
