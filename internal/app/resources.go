package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"ozon-test-task/internal/platform/config"
	"ozon-test-task/internal/platform/postgres"
	shortenerpostgres "ozon-test-task/internal/shortener/adapters/postgres"
	"ozon-test-task/internal/shortener/ports"
)

// resources owns external dependencies shared by HTTP handlers.
type resources struct {
	postgres *pgxpool.Pool
	storage  ports.Storage
}

func newResources(ctx context.Context, cfg config.Config) (*resources, error) {
	r := &resources{}
	if cfg.Storage == "postgres" {
		pool, err := postgres.NewPool(ctx, cfg.Database)
		if err != nil {
			return nil, err
		}
		r.postgres = pool
		r.storage, err = shortenerpostgres.NewStorage(pool, cfg.Database.OperationTimeout)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("create shortener storage: %w", err)
		}
	}
	return r, nil
}

func (r *resources) Check(ctx context.Context) error {
	if r.postgres == nil {
		return nil
	}
	return r.postgres.Ping(ctx)
}

func (r *resources) Close() {
	if r.postgres != nil {
		r.postgres.Close()
	}
}
