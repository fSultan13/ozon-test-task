package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"

	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

var (
	originalsBucket = []byte("originals-v1")
	codesBucket     = []byte("codes-v1")
	ErrClosed       = errors.New("memory storage closed")
)

// Storage uses transactional disk indexes and a bounded FIFO cache of immutable
// links. Eviction affects only the cache, never persisted links. Do not copy it.
// The database file must be on a local filesystem and owned by one process.
// Call Close after requests drain. Writes retain bbolt's default fsync behavior.
// Context bounds writer admission and is checked before transactions; an active
// filesystem syscall cannot be interrupted by context cancellation.
type Storage struct {
	lifecycle  sync.RWMutex
	closed     bool
	db         *bolt.DB
	writer     chan struct{}
	timeout    time.Duration
	cacheMu    sync.RWMutex
	byOriginal map[domain.OriginalURL]domain.Link
	byCode     map[domain.Code]domain.Link
	queue      []domain.Link
	next       int
	capacity   int
}

type Options struct {
	File             string
	CacheEntries     int
	MaxDiskBytes     int
	OperationTimeout time.Duration
	BatchSize        int
	BatchDelay       time.Duration
	MaxPendingWrites int
}

var _ ports.Storage = (*Storage)(nil)

func NewStorage(ctx context.Context, options Options) (*Storage, error) {
	path, cacheEntries, maxDiskBytes, timeout := options.File, options.CacheEntries, options.MaxDiskBytes, options.OperationTimeout
	if path == "" || cacheEntries < 0 || maxDiskBytes < 65536 || timeout <= 0 {
		return nil, errors.New("memory storage requires a path, nonnegative cache size, disk limit >= 65536 and positive timeout")
	}

	if options.BatchSize <= 0 || options.BatchDelay <= 0 || options.BatchDelay >= timeout || options.MaxPendingWrites <= 0 {
		return nil, errors.New("memory storage requires positive batch size, pending write limit and batch delay shorter than operation timeout")
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open memory storage: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("create memory storage directory failed")
	}

	if info, err := os.Stat(path); err == nil && info.Size() > int64(maxDiskBytes) {
		return nil, fmt.Errorf("open memory storage: %w", ports.ErrCapacity)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("stat memory storage failed")
	}

	openTimeout := timeout
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < openTimeout {
		openTimeout = time.Until(deadline)
	}
	if openTimeout <= 0 {
		return nil, fmt.Errorf("open memory storage: %w", context.DeadlineExceeded)
	}

	// Use a stable page size across hosts, including hosts with 16 KiB OS pages.
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: openTimeout, MaxSize: maxDiskBytes, PageSize: 4096})
	if err != nil {
		return nil, diskError("open memory storage", err)
	}

	db.MaxBatchSize, db.MaxBatchDelay = options.BatchSize, options.BatchDelay
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(originalsBucket); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(codesBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, diskError("initialize memory storage", err)
	}

	return &Storage{db: db, writer: make(chan struct{}, options.MaxPendingWrites), timeout: timeout, capacity: cacheEntries,
		byOriginal: make(map[domain.OriginalURL]domain.Link), byCode: make(map[domain.Code]domain.Link)}, nil
}

func (s *Storage) FindByOriginal(ctx context.Context, original domain.OriginalURL) (domain.Link, error) {
	return s.find(ctx, original, domain.Code{}, true)
}

func (s *Storage) FindByCode(ctx context.Context, code domain.Code) (domain.Link, error) {
	return s.find(ctx, domain.OriginalURL{}, code, false)
}

func (s *Storage) find(ctx context.Context, original domain.OriginalURL, code domain.Code, byOriginal bool) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, fmt.Errorf("find memory link: %w", err)
	}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()

	if err := ctx.Err(); err != nil {
		return domain.Link{}, fmt.Errorf("find memory link: %w", err)
	}

	if s.closed {
		return domain.Link{}, ErrClosed
	}

	s.cacheMu.RLock()
	var link domain.Link
	var ok bool
	if byOriginal {
		link, ok = s.byOriginal[original]
	} else {
		link, ok = s.byCode[code]
	}
	s.cacheMu.RUnlock()

	if ok {
		return link, nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	err := s.db.View(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if byOriginal {
			value := tx.Bucket(originalsBucket).Get([]byte(original.String()))

			if value == nil {
				return ports.ErrNotFound
			}

			var err error
			code, err = domain.NewCode(string(value))

			if err != nil {
				return errors.New("invalid stored code")
			}
		}

		value := tx.Bucket(codesBucket).Get([]byte(code.String()))

		if value == nil {
			return ports.ErrNotFound
		}

		storedOriginal, err := domain.NewOriginalURL(string(value))
		if err != nil {
			return errors.New("invalid stored original")
		}

		if byOriginal && storedOriginal != original {
			return errors.New("inconsistent disk indexes")
		}

		link, err = domain.NewLink(storedOriginal, code)
		return err
	})
	if err != nil {
		return domain.Link{}, diskError("find memory link", err)
	}

	s.cache(link)
	return link, nil
}

func (s *Storage) Save(ctx context.Context, link domain.Link) (domain.Link, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return domain.Link{}, fmt.Errorf("save memory link: %w", err)
	}

	if _, err := domain.NewLink(link.OriginalURL(), link.Code()); err != nil {
		return domain.Link{}, fmt.Errorf("save memory link: %w", err)
	}
	// This read also avoids disk writes for an already stored original URL.
	existing, err := s.FindByOriginal(ctx, link.OriginalURL())
	if err == nil {
		return existing, nil
	}

	if !errors.Is(err, ports.ErrNotFound) {
		return domain.Link{}, err
	}

	select {
	case s.writer <- struct{}{}:
		defer func() { <-s.writer }()
	case <-ctx.Done():
		return domain.Link{}, fmt.Errorf("save memory link: %w", ctx.Err())
	}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()

	if s.closed {
		return domain.Link{}, ErrClosed
	}

	saved := link
	err = s.db.Batch(func(tx *bolt.Tx) error {
		// Batch can retry callbacks after another call fails. Keep all effects
		// inside the transaction and recompute the result on each attempt.
		saved = link
		if err := ctx.Err(); err != nil {
			return err
		}

		originals, codes := tx.Bucket(originalsBucket), tx.Bucket(codesBucket)

		original, code := []byte(link.OriginalURL().String()), []byte(link.Code().String())
		if value := originals.Get(original); value != nil {
			existingCode, err := domain.NewCode(string(value))
			if err != nil {
				return errors.New("invalid stored code")
			}

			saved, err = domain.NewLink(link.OriginalURL(), existingCode)
			return err
		}

		if codes.Get(code) != nil {
			return ports.ErrCodeConflict
		}
		if err := originals.Put(original, code); err != nil {
			return err
		}

		return codes.Put(code, original)
	})
	if err != nil {
		return domain.Link{}, diskError("save memory link", err)
	}

	// Publish to the cache only after the entire transaction commits.
	s.cache(saved)
	return saved, nil
}

func (s *Storage) cache(link domain.Link) {
	if s.capacity == 0 {
		return
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if _, ok := s.byCode[link.Code()]; ok {
		return
	}

	if len(s.queue) < s.capacity {
		s.queue = append(s.queue, link)
	} else {
		old := s.queue[s.next]
		delete(s.byCode, old.Code())
		delete(s.byOriginal, old.OriginalURL())
		s.queue[s.next] = link
		s.next = (s.next + 1) % s.capacity
	}

	s.byOriginal[link.OriginalURL()] = link
	s.byCode[link.Code()] = link
}

func (s *Storage) Check(ctx context.Context) error {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return ErrClosed
	}
	return diskError("check memory storage", s.db.View(func(_ *bolt.Tx) error { return ctx.Err() }))
}

func (s *Storage) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	// Close the file even if synchronization fails; report both errors safely.
	syncErr := diskError("sync memory storage", s.db.Sync())
	closeErr := diskError("close memory storage", s.db.Close())
	s.cacheMu.Lock()
	s.byOriginal, s.byCode, s.queue = nil, nil, nil
	s.cacheMu.Unlock()
	return errors.Join(syncErr, closeErr)
}

func diskError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ports.ErrNotFound, ports.ErrCodeConflict, ports.ErrCapacity} {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%s: %w", operation, sentinel)
		}
	}

	if errors.Is(err, berrors.ErrMaxSizeReached) {
		return fmt.Errorf("%s: %w", operation, ports.ErrCapacity)
	}
	// Filesystem and database errors can expose paths and stored input.
	return fmt.Errorf("%s: disk operation failed", operation)
}
