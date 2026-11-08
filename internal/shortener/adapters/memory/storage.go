// Package memory implements a bounded, process-local link store.
package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

var ErrClosed = errors.New("memory storage closed")

// Storage must not be copied. Both indexes share one lock so uniqueness checks
// and insertion are atomic. Links are immutable; readers can use them after unlock.
// Data is retained until Close, with no eviction or background workers.
// Context is checked before and after lock acquisition; mutex waits themselves
// cannot be interrupted. No I/O is performed while holding the lock.
type Storage struct {
	mu         sync.RWMutex
	byOriginal map[domain.OriginalURL]domain.Link
	byCode     map[domain.Code]domain.Link
	maxEntries int
	closed     bool
}

type Options struct {
	// MaxEntries limits associations, not process RSS. Maps grow on demand.
	MaxEntries int
}

var _ ports.Storage = (*Storage)(nil)

func NewStorage(ctx context.Context, options Options) (*Storage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("create memory storage: %w", err)
	}
	if options.MaxEntries <= 0 {
		return nil, errors.New("memory storage requires a positive entry limit")
	}
	return &Storage{byOriginal: make(map[domain.OriginalURL]domain.Link), byCode: make(map[domain.Code]domain.Link), maxEntries: options.MaxEntries}, nil
}

func (s *Storage) FindByOriginal(ctx context.Context, original domain.OriginalURL) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if s.closed {
		return domain.Link{}, ErrClosed
	}
	link, ok := s.byOriginal[original]
	if !ok {
		return domain.Link{}, ports.ErrNotFound
	}
	return link, nil
}

func (s *Storage) FindByCode(ctx context.Context, code domain.Code) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if s.closed {
		return domain.Link{}, ErrClosed
	}
	link, ok := s.byCode[code]
	if !ok {
		return domain.Link{}, ports.ErrNotFound
	}
	return link, nil
}

func (s *Storage) Save(ctx context.Context, link domain.Link) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if _, err := domain.NewLink(link.OriginalURL(), link.Code()); err != nil {
		return domain.Link{}, fmt.Errorf("save memory link: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if s.closed {
		return domain.Link{}, ErrClosed
	}
	// Existing URLs remain usable at capacity, even with a conflicting proposal.
	if existing, ok := s.byOriginal[link.OriginalURL()]; ok {
		return existing, nil
	}
	if _, ok := s.byCode[link.Code()]; ok {
		return domain.Link{}, ports.ErrCodeConflict
	}
	if len(s.byCode) >= s.maxEntries {
		return domain.Link{}, ports.ErrCapacity
	}
	s.byOriginal[link.OriginalURL()] = link
	s.byCode[link.Code()] = link
	return link, nil
}

func (s *Storage) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return ErrClosed
	}
	return nil
}

// Close releases references to links and is safe alongside reads and writes.
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.byOriginal, s.byCode = nil, nil
	return nil
}
