package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ozon-test-task/internal/shortener/adapters/postgres/sqlc"
	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

// Storage shares a concurrency-safe pool and holds no mutable request state.
// The pool belongs to the caller and must outlive Storage.
type Storage struct {
	queries *sqlc.Queries
	timeout time.Duration
}

var _ ports.Storage = (*Storage)(nil)

func NewStorage(pool *pgxpool.Pool, timeout time.Duration) (*Storage, error) {
	if pool == nil || timeout <= 0 {
		return nil, errors.New("postgres storage requires a pool and a positive operation timeout")
	}
	return &Storage{queries: sqlc.New(pool), timeout: timeout}, nil
}

func (s *Storage) FindByOriginal(ctx context.Context, original domain.OriginalURL) (domain.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	row, err := s.queries.FindByOriginal(ctx, original.String())
	return decode(ctx, row, err, "find original")
}

func (s *Storage) FindByCode(ctx context.Context, code domain.Code) (domain.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	row, err := s.queries.FindByCode(ctx, code.String())
	return decode(ctx, row, err, "find code")
}

func (s *Storage) Save(ctx context.Context, link domain.Link) (domain.Link, error) {
	// Reject zero values before touching the database.
	if _, err := domain.NewLink(link.OriginalURL(), link.Code()); err != nil {
		return domain.Link{}, fmt.Errorf("save link: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	row, err := s.queries.InsertLink(ctx, sqlc.InsertLinkParams{
		OriginalUrl: link.OriginalURL().String(), Code: link.Code().String(),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		return decode(ctx, row, err, "insert link")
	}

	// A separate statement gets a fresh READ COMMITTED snapshot after INSERT
	// has waited for a competing writer. A same-statement CTE could miss it.
	// Prefer an existing original even when the proposed code also conflicts.
	row, err = s.queries.FindByOriginalHash(ctx, link.OriginalURL().String())
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, ports.ErrCodeConflict
	}
	if err != nil {
		return domain.Link{}, storageError(ctx, "find conflicting original", err)
	}
	if row.OriginalUrl != link.OriginalURL().String() {
		// Never return a different URL on a SHA-256 collision.
		return domain.Link{}, errors.New("save link: original URL digest collision")
	}
	return decode(ctx, row, nil, "read existing link")
}

func decode(ctx context.Context, row sqlc.Link, err error, operation string) (domain.Link, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, fmt.Errorf("%s: %w", operation, ports.ErrNotFound)
	}
	if err != nil {
		return domain.Link{}, storageError(ctx, operation, err)
	}
	original, err := domain.NewOriginalURL(row.OriginalUrl)
	if err != nil {
		return domain.Link{}, fmt.Errorf("%s: invalid stored original URL", operation)
	}
	code, err := domain.NewCode(row.Code)
	if err != nil {
		return domain.Link{}, fmt.Errorf("%s: invalid stored code", operation)
	}
	return domain.NewLink(original, code)
}

func storageError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", operation, ctx.Err())
	}
	// PgError.Detail and driver errors may contain URLs or credentials.
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, context.DeadlineExceeded)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%s: postgres error (SQLSTATE %s)", operation, pgErr.Code)
	}
	return fmt.Errorf("%s: postgres operation failed", operation)
}
