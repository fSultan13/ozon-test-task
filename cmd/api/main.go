package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"ozon-test-task/internal/app"
	"ozon-test-task/internal/platform/config"
	"ozon-test-task/internal/platform/logger"
	"syscall"
)

// @title URL Shortener API
// @version 0.1
// @description Infrastructure scaffold. URL shortening is not implemented yet.
// @BasePath /
func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println("Usage: api [-storage=memory|postgres] [-http-addr=:8080]\nConfiguration: see README.md and .env.example")
		return nil
	}
	if err != nil {
		return err
	}
	log := logger.New(os.Stdout, cfg.Log)
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg, log)
}
