package random

import (
	"context"
	"errors"
	"sync"
	"testing"

	"ozon-test-task/internal/shortener/domain"
)

func TestGenerateConcurrent(t *testing.T) {
	const workers, perWorker = 32, 100
	var generator Generator
	var wg sync.WaitGroup

	codes := make(chan domain.Code, workers*perWorker)

	for range workers {
		wg.Go(func() {
			for range perWorker {
				code, err := generator.Generate(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				codes <- code
			}
		})
	}

	wg.Wait()
	close(codes)

	count := 0
	for code := range codes {
		count++
		if len(code.String()) != domain.MaxCodeLength {
			t.Fatal("incorrect code length")
		}

		if _, err := domain.NewCode(code.String()); err != nil {
			t.Fatal(err)
		}
	}

	if count != workers*perWorker {
		t.Fatalf("generated %d codes", count)
	}
	// Randomness is not a uniqueness guarantee. Collision handling is tested
	// deterministically in application tests rather than probabilistically here.
}

func TestGenerateCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Generator{}).Generate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func BenchmarkGenerate(b *testing.B) {
	g := Generator{}
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := g.Generate(ctx); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
