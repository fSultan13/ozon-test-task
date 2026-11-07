package logger

import (
	"io"
	"log/slog"
	"ozon-test-task/internal/platform/config"
)

func New(w io.Writer, cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
