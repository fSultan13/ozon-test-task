package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ozon-test-task/internal/shortener/domain"
)

func saveConcurrent(b *testing.B, s *Storage, links []domain.Link) {
	b.Helper()
	var wg sync.WaitGroup
	for _, l := range links {
		wg.Go(func() {
			if got, err := s.Save(context.Background(), l); err != nil || got != l {
				b.Errorf("save = %v, %v", got, err)
			}
		})
	}
	wg.Wait()
}

func BenchmarkStorage(b *testing.B) {
	for _, capacity := range []int{0, 256} {
		b.Run(fmt.Sprintf("Cache%d", capacity), func(b *testing.B) {
			s := openStorage(b, filepath.Join(b.TempDir(), "links.db"), capacity, 16<<20)

			links := make([]domain.Link, 256)
			for i := range links {
				links[i] = testLink(b, fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("c%d", i))
			}

			saveConcurrent(b, s, links)

			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					l := links[i%len(links)]
					got, err := s.FindByCode(ctx, l.Code())
					if err != nil || got != l {
						b.Error("unexpected result")
						return
					}
					i++
				}
			})
		})
	}
}

// Each operation creates/closes a file and saves 32 distinct links concurrently.
// Comparing Batch1 and Batch128 keeps durability and the workload identical.
func BenchmarkStorageConcurrentInsertBatch(b *testing.B) {
	links := make([]domain.Link, 32)
	for i := range links {
		links[i] = testLink(b, fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("c%d", i))
	}

	for _, batchSize := range []int{1, 128} {
		b.Run(fmt.Sprintf("Batch%d", batchSize), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "links.db")
			options := testOptions(path, 32, 1<<20, 3*time.Second)
			options.BatchSize = batchSize
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				s, err := NewStorage(context.Background(), options)
				if err != nil {
					b.Fatal(err)
				}
				saveConcurrent(b, s, links)
				if err := s.Close(); err != nil {
					b.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
