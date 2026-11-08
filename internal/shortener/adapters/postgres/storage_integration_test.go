package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ozon-test-task/internal/shortener/adapters/random"
	"ozon-test-task/internal/shortener/application"
	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

func testPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		tb.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatal("invalid test database configuration")
	}

	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		tb.Fatal("create test pool failed")
	}

	tb.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		tb.Fatal("test database unavailable")
	}
	return pool
}

func testStorage(tb testing.TB, pool *pgxpool.Pool) *Storage {
	tb.Helper()
	s, err := NewStorage(pool, 5*time.Second)
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

func testLink(tb testing.TB, original, code string) domain.Link {
	tb.Helper()
	u, err := domain.NewOriginalURL(original)
	if err != nil {
		tb.Fatal(err)
	}
	c, err := domain.NewCode(code)
	if err != nil {
		tb.Fatal(err)
	}
	l, err := domain.NewLink(u, c)
	if err != nil {
		tb.Fatal(err)
	}
	return l
}

func resetLinks(tb testing.TB, pool *pgxpool.Pool) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "TRUNCATE links"); err != nil {
		tb.Fatal("truncate test links failed")
	}
}

func TestStorageIntegration(t *testing.T) {
	pool := testPool(t)
	s := testStorage(t, pool)
	ctx := context.Background()

	t.Run("round trip and conflicts", func(t *testing.T) {
		resetLinks(t, pool)
		first := testLink(t, "https://example.com/a?x=1#part", "Abc_123456")
		second := testLink(t, "https://example.com/b", "OtherCode1")
		for _, l := range []domain.Link{first, second} {
			if got, err := s.Save(ctx, l); err != nil || got != l {
				t.Fatalf("save: %v", err)
			}
			if got, err := s.FindByOriginal(ctx, l.OriginalURL()); err != nil || got != l {
				t.Fatalf("original lookup: %v", err)
			}
			if got, err := s.FindByCode(ctx, l.Code()); err != nil || got != l {
				t.Fatalf("code lookup: %v", err)
			}
		}
		// An existing original wins even if the suggested code belongs to another URL.
		duplicate := testLink(t, first.OriginalURL().String(), second.Code().String())
		if got, err := s.Save(ctx, duplicate); err != nil || got != first {
			t.Fatalf("duplicate: %v", err)
		}

		conflict := testLink(t, "https://example.com/c", first.Code().String())
		if _, err := s.Save(ctx, conflict); !errors.Is(err, ports.ErrCodeConflict) {
			t.Fatalf("conflict: %v", err)
		}
		if _, err := s.FindByOriginal(ctx, conflict.OriginalURL()); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("conflict modified storage: %v", err)
		}

		missing := testLink(t, "https://example.com/missing", "missing")
		if _, err := s.FindByCode(ctx, missing.Code()); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("missing code: %v", err)
		}
	})

	t.Run("long URLs and exact equality", func(t *testing.T) {
		resetLinks(t, pool)
		prefix := "https://example.com/"
		for i, raw := range []string{
			prefix + strings.Repeat("a", domain.MaxOriginalURLBytes-len(prefix)),
			"https://example.com/A", "https://example.com/a", "https://example.com/%41",
			"https://example.com/?q=привет", "https://example.com/?q=\\test",
		} {
			l := testLink(t, raw, fmt.Sprintf("code%d", i))
			if _, err := s.Save(ctx, l); err != nil {
				t.Fatal(err)
			}
			if got, err := s.FindByOriginal(ctx, l.OriginalURL()); err != nil || got != l {
				t.Fatalf("exact lookup: %v", err)
			}
		}
	})

	t.Run("concurrent same original across pools", func(t *testing.T) {
		resetLinks(t, pool)
		other := testStorage(t, testPool(t))
		const workers = 64

		results := make(chan domain.Link, workers)
		start := make(chan struct{})

		var wg sync.WaitGroup

		for i := range workers {
			l := testLink(t, "https://example.com/shared", fmt.Sprintf("code%d", i))
			current := s
			if i%2 == 0 {
				current = other
			}
			wg.Go(func() {
				<-start
				got, err := current.Save(ctx, l)
				if err != nil {
					t.Error(err)
					return
				}
				results <- got
			})
		}

		close(start)
		wg.Wait()
		close(results)
		var expected domain.Link
		count := 0
		for got := range results {
			if count == 0 {
				expected = got
			}
			if got != expected {
				t.Error("same original received different codes")
			}
			count++
		}
		if count != workers {
			t.Fatalf("results=%d", count)
		}
		var rows int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM links").Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("rows=%d: %v", rows, err)
		}
	})

	t.Run("concurrent same code", func(t *testing.T) {
		resetLinks(t, pool)
		const workers = 32
		start := make(chan struct{})
		successes := make(chan domain.Link, workers)
		var wg sync.WaitGroup
		for i := range workers {
			l := testLink(t, fmt.Sprintf("https://example.com/%d", i), "samecode")
			wg.Go(func() {
				<-start
				got, err := s.Save(ctx, l)
				if err == nil {
					successes <- got
				} else if !errors.Is(err, ports.ErrCodeConflict) {
					t.Error(err)
				}
			})
		}
		close(start)
		wg.Wait()
		close(successes)
		if len(successes) != 1 {
			t.Fatalf("successful inserts=%d", len(successes))
		}
	})

	t.Run("application with real storage", func(t *testing.T) {
		resetLinks(t, pool)
		service, err := application.NewService(s, random.Generator{}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		link, err := service.Shorten(ctx, "https://example.com/service")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := service.Resolve(ctx, link.Code().String()); err != nil || got != link {
			t.Fatalf("resolve: %v", err)
		}
	})

	t.Run("cancellation and timeout under database lock", func(t *testing.T) {
		resetLinks(t, pool)
		l := testLink(t, "https://example.com/timeout", "timeout")
		canceled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := s.Save(canceled, l); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel save: %v", err)
		}
		if _, err := s.FindByOriginal(canceled, l.OriginalURL()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel find: %v", err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if _, err := tx.Exec(ctx, "LOCK TABLE links IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}

		short, err := NewStorage(pool, 30*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := short.Save(ctx, l); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("save timeout: %v", err)
		}

		if _, err := short.FindByCode(ctx, l.Code()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("find timeout: %v", err)
		}
	})
}

func BenchmarkStoragePostgres(b *testing.B) {
	pool := testPool(b)
	s := testStorage(b, pool)
	resetLinks(b, pool)
	l := testLink(b, "https://example.com/benchmark", "benchmark")

	if _, err := s.Save(context.Background(), l); err != nil {
		b.Fatal(err)
	}

	for _, operation := range []string{"resolve", "save_existing"} {
		b.Run(operation, func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					var err error
					if operation == "resolve" {
						_, err = s.FindByCode(context.Background(), l.Code())
					} else {
						_, err = s.Save(context.Background(), l)
					}
					if err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}
