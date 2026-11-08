package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

func testLink(t testing.TB, original, code string) domain.Link {
	t.Helper()
	u, err := domain.NewOriginalURL(original)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.NewCode(code)
	if err != nil {
		t.Fatal(err)
	}
	l, err := domain.NewLink(u, c)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func openStorage(t testing.TB, path string, cache, disk int) *Storage {
	t.Helper()
	s, err := NewStorage(context.Background(), testOptions(path, cache, disk, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestStorageEvictionAndRecovery(t *testing.T) {
	for _, capacity := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "links.db")
			s := openStorage(t, path, capacity, 1<<20)
			links := make([]domain.Link, 10)
			ctx := context.Background()
			for i := range links {
				links[i] = testLink(t, fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("c%d", i))
				if got, err := s.Save(ctx, links[i]); err != nil || got != links[i] {
					t.Fatalf("save = %v, %v", got, err)
				}
			}
			assertLinks(t, s, links)
			if len(s.queue) > capacity || len(s.byCode) > capacity || len(s.byOriginal) > capacity {
				t.Fatal("cache capacity exceeded")
			}
			duplicate := testLink(t, links[0].OriginalURL().String(), links[1].Code().String())
			if got, err := s.Save(ctx, duplicate); err != nil || got != links[0] {
				t.Fatalf("duplicate = %v, %v", got, err)
			}
			unused := testLink(t, links[0].OriginalURL().String(), "unused")
			if got, err := s.Save(ctx, unused); err != nil || got != links[0] {
				t.Fatalf("duplicate with unused code = %v, %v", got, err)
			}
			if _, err := s.FindByCode(ctx, unused.Code()); !errors.Is(err, ports.ErrNotFound) {
				t.Fatal("duplicate created an alias")
			}
			conflict := testLink(t, "https://example.com/conflict", links[0].Code().String())
			if _, err := s.Save(ctx, conflict); !errors.Is(err, ports.ErrCodeConflict) {
				t.Fatal(err)
			}
			if _, err := s.FindByOriginal(ctx, conflict.OriginalURL()); !errors.Is(err, ports.ErrNotFound) {
				t.Fatal("conflict persisted")
			}
			if _, err := s.Save(ctx, domain.Link{}); !errors.Is(err, domain.ErrInvalidOriginalURL) {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.FindByCode(ctx, links[0].Code()); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			if _, err := s.Save(ctx, links[0]); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			if err := s.Check(ctx); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			restored := openStorage(t, path, capacity, 1<<20)
			if len(restored.queue) != 0 {
				t.Fatal("recovery preloaded all links into RAM")
			}
			assertLinks(t, restored, links)
			if err := restored.Check(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertLinks(t *testing.T, s *Storage, links []domain.Link) {
	t.Helper()
	for _, l := range links {
		if got, err := s.FindByCode(context.Background(), l.Code()); err != nil || got != l {
			t.Fatalf("code = %v, %v", got, err)
		}
		if got, err := s.FindByOriginal(context.Background(), l.OriginalURL()); err != nil || got != l {
			t.Fatalf("original = %v, %v", got, err)
		}
	}
}

func TestStorageDiskLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.db")
	s := openStorage(t, path, 1, 65536)
	var saved []domain.Link
	full := false
	for i := range 1000 {
		l := testLink(t, fmt.Sprintf("https://example.com/%0500d", i), fmt.Sprintf("c%d", i))
		_, err := s.Save(context.Background(), l)
		if errors.Is(err, ports.ErrCapacity) {
			full = true
			if _, err := s.FindByOriginal(context.Background(), l.OriginalURL()); !errors.Is(err, ports.ErrNotFound) {
				t.Fatal("partial insert")
			}
			if _, err := s.FindByCode(context.Background(), l.Code()); !errors.Is(err, ports.ErrNotFound) {
				t.Fatal("partial insert")
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, l)
	}
	if !full || len(saved) == 0 {
		t.Fatal("did not exercise disk limit")
	}
	assertLinks(t, s, saved)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 65536 {
		t.Fatalf("file exceeded limit: %d", info.Size())
	}
	// Existing originals must remain usable even at capacity.
	if _, err := s.Save(context.Background(), saved[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored := openStorage(t, path, 1, 65536)
	assertLinks(t, restored, saved)
}

func TestStorageConcurrent(t *testing.T) {
	for _, mode := range []string{"same original", "same code", "distinct"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "links.db")
			s := openStorage(t, path, 4, 1<<20)
			const n = 32
			links := make([]domain.Link, n)
			for i := range links {
				original, code := fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("c%d", i)
				if mode == "same original" {
					original = "https://example.com/shared"
				}
				if mode == "same code" {
					code = "shared"
				}
				links[i] = testLink(t, original, code)
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make(chan domain.Link, n)
			for _, l := range links {
				wg.Go(func() {
					<-start
					got, err := s.Save(context.Background(), l)
					if errors.Is(err, ports.ErrCodeConflict) && mode == "same code" {
						return
					}
					if err != nil {
						t.Error(err)
						return
					}
					results <- got
					for range 5 {
						byCode, err := s.FindByCode(context.Background(), got.Code())
						if err != nil || byCode != got {
							t.Errorf("read code: %v", err)
						}
						byOriginal, err := s.FindByOriginal(context.Background(), got.OriginalURL())
						if err != nil || byOriginal != got {
							t.Errorf("read original: %v", err)
						}
					}
				})
			}
			close(start)
			wg.Wait()
			close(results)
			unique := make(map[domain.Link]bool)
			for l := range results {
				unique[l] = true
			}
			want := 1
			if mode == "distinct" {
				want = n
			}
			if len(unique) != want {
				t.Fatalf("unique links = %d, want %d", len(unique), want)
			}
			if len(s.queue) > s.capacity || len(s.byCode) > s.capacity || len(s.byOriginal) > s.capacity {
				t.Fatal("cache capacity exceeded")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			restored := openStorage(t, path, 0, 1<<20)
			for l := range unique {
				assertLinks(t, restored, []domain.Link{l})
			}
		})
	}
}

func TestStorageCancellationAndFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.db")
	s := openStorage(t, path, 1, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := testLink(t, "https://example.com", "a")
	if _, err := s.Save(ctx, l); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.FindByCode(ctx, l.Code()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.FindByOriginal(ctx, l.OriginalURL()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewStorage(ctx, testOptions(path, 1, 1<<20, time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A second process/instance cannot bypass the transactional uniqueness checks.
	if other, err := NewStorage(context.Background(), testOptions(path, 1, 1<<20, 10*time.Millisecond)); err == nil {
		_ = other.Close()
		t.Fatal("opened locked file")
	}
	// Writer admission is bounded by the caller's deadline.
	for range cap(s.writer) {
		s.writer <- struct{}{}
	}
	short, cancelShort := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelShort()
	if _, err := s.Save(short, l); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	for range cap(s.writer) {
		<-s.writer
	}
	if _, err := s.FindByCode(context.Background(), l.Code()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal("canceled insert persisted")
	}
}

func testOptions(path string, cache, disk int, timeout time.Duration) Options {
	return Options{File: path, CacheEntries: cache, MaxDiskBytes: disk, OperationTimeout: timeout, BatchSize: 128, BatchDelay: time.Millisecond, MaxPendingWrites: 1024}
}

func TestConcurrentWritesCommitInOneBatch(t *testing.T) {
	const n = 16
	options := testOptions(filepath.Join(t.TempDir(), "links.db"), n, 1<<20, 5*time.Second)
	options.BatchSize, options.BatchDelay = n, time.Second
	s, err := NewStorage(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	var before int
	if err := s.db.View(func(tx *bolt.Tx) error { before = tx.ID(); return nil }); err != nil {
		t.Fatal(err)
	}
	links := make([]domain.Link, n)
	for i := range links {
		links[i] = testLink(t, fmt.Sprintf("https://example.com/batch/%d", i), fmt.Sprintf("b%d", i))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, link := range links {
		wg.Go(func() {
			<-start
			if got, err := s.Save(context.Background(), link); err != nil || got != link {
				t.Errorf("save = %v, %v", got, err)
			}
		})
	}
	close(start)
	wg.Wait()
	var after int
	if err := s.db.View(func(tx *bolt.Tx) error { after = tx.ID(); return nil }); err != nil {
		t.Fatal(err)
	}
	if after-before != 1 {
		t.Fatalf("committed %d transactions for %d concurrent inserts; want 1", after-before, n)
	}
	assertLinks(t, s, links)
}
