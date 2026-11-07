package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"ozon-test-task/internal/platform/config"
)

func NewPool(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("invalid postgres connection configuration")
	}

	pc.MaxConns = cfg.MaxConns
	pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, errors.New("create postgres pool failed")
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		// Driver errors may contain the DSN, username or credentials.
		return nil, errors.New("postgres startup check failed")
	}

	return pool, nil
}
