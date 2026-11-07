package app

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"ozon-test-task/internal/platform/config"
	"ozon-test-task/internal/platform/postgres"
)

// resources owns external dependencies shared by HTTP handlers.
type resources struct {
	postgres *pgxpool.Pool
}

func newResources(ctx context.Context, cfg config.Config) (*resources, error) {
	r := &resources{}
	if cfg.Storage == "postgres" {
		pool, err := postgres.NewPool(ctx, cfg.Database)
		if err != nil {
			return nil, err
		}
		r.postgres = pool
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
