package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

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

func openStorage(t testing.TB, limit int) *Storage {
	t.Helper()
	s, err := NewStorage(context.Background(), Options{MaxEntries: limit})
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

func assertLink(t testing.TB, s *Storage, l domain.Link) {
	t.Helper()
	if got, err := s.FindByCode(context.Background(), l.Code()); err != nil || got != l {
		t.Errorf("find code = %v, %v", got, err)
	}
	if got, err := s.FindByOriginal(context.Background(), l.OriginalURL()); err != nil || got != l {
		t.Errorf("find original = %v, %v", got, err)
	}
}

func TestStorageUniquenessAndCapacity(t *testing.T) {
	ctx := context.Background()
	s := openStorage(t, 2)
	a := testLink(t, "https://example.com/a", "aaaaaaaaaa")
	b := testLink(t, "https://example.com/b", "bbbbbbbbbb")
	c := testLink(t, "https://example.com/c", "cccccccccc")
	if _, err := s.FindByCode(ctx, a.Code()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.FindByOriginal(ctx, a.OriginalURL()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
	for _, l := range []domain.Link{a, b} {
		if got, err := s.Save(ctx, l); err != nil || got != l {
			t.Fatalf("save = %v, %v", got, err)
		}
	}
	for _, code := range []string{b.Code().String(), c.Code().String()} {
		duplicate := testLink(t, a.OriginalURL().String(), code)
		if got, err := s.Save(ctx, duplicate); err != nil || got != a {
			t.Fatalf("duplicate at capacity = %v, %v", got, err)
		}
	}
	conflict := testLink(t, c.OriginalURL().String(), a.Code().String())
	if _, err := s.Save(ctx, conflict); !errors.Is(err, ports.ErrCodeConflict) {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, c); !errors.Is(err, ports.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.FindByOriginal(ctx, c.OriginalURL()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal("failed write created original", err)
	}
	if _, err := s.FindByCode(ctx, c.Code()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal("failed write created code", err)
	}
	assertLink(t, s, a)
	assertLink(t, s, b)
	if _, err := s.Save(ctx, domain.Link{}); !errors.Is(err, domain.ErrInvalidOriginalURL) {
		t.Fatal(err)
	}
}

func TestStorageConcurrent(t *testing.T) {
	for _, mode := range []string{"same original", "same code", "distinct", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			const n = 256
			limit := n
			if mode == "capacity" {
				limit = 17
			}
			s := openStorage(t, limit)
			links := make([]domain.Link, n)
			for i := range links {
				original, code := fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("%010d", i)
				if mode == "same original" {
					original = "https://example.com/shared"
				}
				if mode == "same code" {
					code = "sharedcode"
				}
				links[i] = testLink(t, original, code)
			}
			var wg sync.WaitGroup
			var successes atomic.Int64
			results := make(chan domain.Link, n)
			start := make(chan struct{})
			for _, l := range links {
				wg.Go(func() {
					<-start
					got, err := s.Save(context.Background(), l)
					if mode == "same code" && errors.Is(err, ports.ErrCodeConflict) {
						return
					}
					if mode == "capacity" && errors.Is(err, ports.ErrCapacity) {
						return
					}
					if err != nil {
						t.Error(err)
						return
					}
					successes.Add(1)
					results <- got
					for range 5 {
						assertLink(t, s, got)
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
			wantUnique, wantSuccess := n, n
			switch mode {
			case "same original":
				wantUnique = 1
			case "same code":
				wantUnique, wantSuccess = 1, 1
			case "capacity":
				wantUnique, wantSuccess = limit, limit
			}
			if len(unique) != wantUnique || int(successes.Load()) != wantSuccess {
				t.Fatalf("unique=%d successes=%d, want %d/%d", len(unique), successes.Load(), wantUnique, wantSuccess)
			}
			// After contention, verify both directions for every attempted association.
			for _, l := range links {
				got, err := s.FindByOriginal(context.Background(), l.OriginalURL())
				if err == nil {
					assertLink(t, s, got)
				} else if !errors.Is(err, ports.ErrNotFound) {
					t.Fatal(err)
				}
				got, err = s.FindByCode(context.Background(), l.Code())
				if err == nil {
					assertLink(t, s, got)
				} else if !errors.Is(err, ports.ErrNotFound) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStorageCancellationAndClose(t *testing.T) {
	s := openStorage(t, 1)
	l := testLink(t, "https://example.com", "aaaaaaaaaa")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewStorage(ctx, Options{MaxEntries: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1} {
		if _, err := NewStorage(context.Background(), Options{MaxEntries: limit}); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
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
	if _, err := s.FindByCode(context.Background(), l.Code()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, err := s.Save(context.Background(), l)
			if err != nil && !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
			_, err = s.FindByCode(context.Background(), l.Code())
			if err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ports.ErrNotFound) {
				t.Error(err)
			}
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err := s.Save(context.Background(), l); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := s.FindByCode(context.Background(), l.Code()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := s.FindByOriginal(context.Background(), l.OriginalURL()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
