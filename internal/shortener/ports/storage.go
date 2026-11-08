package ports

import (
	"context"
	"errors"

	"ozon-test-task/internal/shortener/domain"
)

var (
	ErrNotFound     = errors.New("link not found")
	ErrCodeConflict = errors.New("code already belongs to another URL")
	ErrCapacity     = errors.New("storage capacity reached")
)

// Storage implementations must support concurrent calls, honor context and
// enforce uniqueness of both the exact original URL and code atomically.
type Storage interface {
	FindByOriginal(context.Context, domain.OriginalURL) (domain.Link, error)
	FindByCode(context.Context, domain.Code) (domain.Link, error)
	// Save returns the existing association if the original URL already exists
	// (even if the proposed code conflicts). Otherwise it inserts the link or
	// returns ErrCodeConflict without modifying storage. This must be atomic
	// across all callers, including separate service instances.
	Save(context.Context, domain.Link) (domain.Link, error)
}
