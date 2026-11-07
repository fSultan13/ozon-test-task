package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ozon-test-task/internal/shortener/adapters/random"
	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

type storageStub struct {
	findOriginal func(context.Context, domain.OriginalURL) (domain.Link, error)
	findCode     func(context.Context, domain.Code) (domain.Link, error)
	save         func(context.Context, domain.Link) (domain.Link, error)
}

func (s storageStub) FindByOriginal(ctx context.Context, u domain.OriginalURL) (domain.Link, error) {
	return s.findOriginal(ctx, u)
}

func (s storageStub) FindByCode(ctx context.Context, c domain.Code) (domain.Link, error) {
	return s.findCode(ctx, c)
}

func (s storageStub) Save(ctx context.Context, l domain.Link) (domain.Link, error) {
	return s.save(ctx, l)
}

type generatorFunc func(context.Context) (domain.Code, error)

func (f generatorFunc) Generate(ctx context.Context) (domain.Code, error) { return f(ctx) }

func newService(t *testing.T, storage ports.Storage, generator ports.CodeGenerator) *Service {
	t.Helper()
	s, err := NewService(storage, generator, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func missing(context.Context, domain.OriginalURL) (domain.Link, error) {
	return domain.Link{}, fmt.Errorf("lookup: %w", ports.ErrNotFound)
}

func TestShortenCollisionAndResolve(t *testing.T) {
	var saved domain.Link
	attempts := 0
	storage := storageStub{
		findOriginal: missing,
		save: func(_ context.Context, link domain.Link) (domain.Link, error) {
			attempts++
			if attempts == 1 {
				return domain.Link{}, fmt.Errorf("insert: %w", ports.ErrCodeConflict)
			}
			saved = link
			return link, nil
		},
		findCode: func(_ context.Context, code domain.Code) (domain.Link, error) {
			if code != saved.Code() {
				return domain.Link{}, ports.ErrNotFound
			}
			return saved, nil
		},
	}

	s := newService(t, storage, random.Generator{})
	link, err := s.Shorten(context.Background(), "https://example.com/a?x=1#part")

	if err != nil || attempts != 2 {
		t.Fatalf("shorten: attempts=%d error=%v", attempts, err)
	}

	resolved, err := s.Resolve(context.Background(), link.Code().String())
	if err != nil || resolved != link || resolved.OriginalURL().String() != "https://example.com/a?x=1#part" {
		t.Fatalf("resolve did not preserve association: %v", err)
	}

	if _, err := s.Resolve(context.Background(), "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestShortenFailures(t *testing.T) {
	failure := errors.New("dependency failed")

	for _, tt := range []struct {
		name                                string
		findErr, generateErr, saveErr, want error
		zeroCode                            bool
		wantGenerations                     int
	}{
		{name: "lookup failure", findErr: failure, want: failure},
		{name: "generation failure", findErr: ports.ErrNotFound, generateErr: failure, want: failure, wantGenerations: 1},
		{name: "save failure", findErr: ports.ErrNotFound, saveErr: failure, want: failure, wantGenerations: 1},
		{name: "collisions exhausted", findErr: ports.ErrNotFound, saveErr: ports.ErrCodeConflict, want: ErrGenerationExhausted, wantGenerations: maxGenerationAttempts},
		{name: "invalid generator output", findErr: ports.ErrNotFound, zeroCode: true, want: domain.ErrInvalidCode, wantGenerations: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			generations := 0
			storage := storageStub{
				findOriginal: func(context.Context, domain.OriginalURL) (domain.Link, error) { return domain.Link{}, tt.findErr },
				save:         func(context.Context, domain.Link) (domain.Link, error) { return domain.Link{}, tt.saveErr },
			}
			generator := generatorFunc(func(context.Context) (domain.Code, error) {
				generations++
				if tt.generateErr != nil || tt.zeroCode {
					return domain.Code{}, tt.generateErr
				}
				return domain.NewCode("abcdefghij")
			})

			_, err := newService(t, storage, generator).Shorten(context.Background(), "https://example.com")
			if !errors.Is(err, tt.want) || generations != tt.wantGenerations {
				t.Fatalf("error=%v, generations=%d", err, generations)
			}
		})
	}
}

func TestExistingOriginalDoesNotGenerate(t *testing.T) {
	original, err := domain.NewOriginalURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	code, err := domain.NewCode("existing")
	if err != nil {
		t.Fatal(err)
	}

	existing, err := domain.NewLink(original, code)
	if err != nil {
		t.Fatal(err)
	}

	storage := storageStub{findOriginal: func(context.Context, domain.OriginalURL) (domain.Link, error) { return existing, nil }}

	// Nil stub functions panic if an unexpected generation or save is attempted.
	s := newService(t, storage, generatorFunc(nil))
	got, err := s.Shorten(context.Background(), original.String())
	if err != nil || got != existing {
		t.Fatalf("existing link: %v", err)
	}
}

func TestValidationAndCancellationBeforeDependencies(t *testing.T) {
	s := newService(t, storageStub{}, generatorFunc(nil))

	for _, raw := range []string{"", "example.com", "ftp://example.com", "https://example.com/a b", "https://example.com/?q=%zz"} {
		if _, err := s.Shorten(context.Background(), raw); !errors.Is(err, domain.ErrInvalidOriginalURL) {
			t.Fatal(err)
		}
	}
	if _, err := s.Resolve(context.Background(), "bad-code"); !errors.Is(err, domain.ErrInvalidCode) {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Shorten(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, "valid"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOperationTimeoutAndParentDeadline(t *testing.T) {
	for _, parentEarlier := range []bool{false, true} {
		t.Run(fmt.Sprint(parentEarlier), func(t *testing.T) {
			storage := storageStub{findOriginal: func(ctx context.Context, _ domain.OriginalURL) (domain.Link, error) {
				<-ctx.Done()
				return domain.Link{}, ctx.Err()
			}}
			timeout, parentTimeout := 10*time.Millisecond, time.Second
			if parentEarlier {
				timeout, parentTimeout = parentTimeout, timeout
			}

			s, err := NewService(storage, random.Generator{}, timeout)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
			defer cancel()

			if _, err := s.Shorten(ctx, "https://example.com"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestNewServiceValidation(t *testing.T) {
	for _, tt := range []struct {
		storage   ports.Storage
		generator ports.CodeGenerator
		timeout   time.Duration
	}{
		{nil, random.Generator{}, time.Second},
		{storageStub{}, nil, time.Second},
		{storageStub{}, random.Generator{}, 0},
		{storageStub{}, random.Generator{}, -time.Second},
	} {
		if _, err := NewService(tt.storage, tt.generator, tt.timeout); err == nil {
			t.Fatal("invalid dependencies accepted")
		}
	}
}

func TestConcurrentShortenAcrossServices(t *testing.T) {
	// A test-only atomic storage models the port contract. A barrier makes all
	// workers miss the initial lookup so Save must resolve the insertion race.
	const workers = 32

	var mu sync.Mutex

	links := make(map[domain.OriginalURL]domain.Link)
	codes := make(map[domain.Code]domain.Link)

	var lookups sync.WaitGroup

	lookups.Add(workers)

	storage := storageStub{
		findOriginal: func(context.Context, domain.OriginalURL) (domain.Link, error) {
			lookups.Done()
			lookups.Wait()
			return domain.Link{}, ports.ErrNotFound
		},
		save: func(_ context.Context, link domain.Link) (domain.Link, error) {
			mu.Lock()
			defer mu.Unlock()

			if existing, ok := links[link.OriginalURL()]; ok {
				return existing, nil
			}
			if _, ok := codes[link.Code()]; ok {
				return domain.Link{}, ports.ErrCodeConflict
			}

			links[link.OriginalURL()] = link
			codes[link.Code()] = link
			return link, nil
		},
	}

	first := newService(t, storage, random.Generator{})
	second := newService(t, storage, random.Generator{})
	results := make(chan domain.Link, workers)

	var wg sync.WaitGroup

	for i := range workers {
		s := first
		if i%2 == 0 {
			s = second
		}
		wg.Go(func() {
			link, err := s.Shorten(context.Background(), "https://example.com")
			if err != nil {
				t.Error(err)
				return
			}
			results <- link
		})
	}
	wg.Wait()

	close(results)

	var expected domain.Link

	count := 0

	for link := range results {
		if count == 0 {
			expected = link
		}
		if link != expected {
			t.Fatal("same URL received different codes")
		}
		count++
	}

	if count != workers || len(links) != 1 || len(codes) != 1 {
		t.Fatalf("results=%d, links=%d, codes=%d", count, len(links), len(codes))
	}
}
