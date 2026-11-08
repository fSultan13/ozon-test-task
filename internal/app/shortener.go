package app

import (
	"fmt"

	"ozon-test-task/internal/platform/config"
	shortenerhttp "ozon-test-task/internal/shortener/adapters/http"
	"ozon-test-task/internal/shortener/adapters/random"
	"ozon-test-task/internal/shortener/application"
	"ozon-test-task/internal/shortener/ports"
)

// newShortenerHandler wires the shortener context using the selected storage.
func newShortenerHandler(cfg config.Config, storage ports.Storage) (*shortenerhttp.Handler, error) {
	service, err := application.NewService(storage, random.Generator{}, cfg.ShortenerOperationTimeout)
	if err != nil {
		return nil, fmt.Errorf("create shortener service: %w", err)
	}
	return shortenerhttp.NewHandler(service, cfg.PublicBaseURL), nil
}
