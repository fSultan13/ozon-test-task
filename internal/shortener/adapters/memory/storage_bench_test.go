package memory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ozon-test-task/internal/shortener/domain"
)

func BenchmarkStorage(b *testing.B) {
	const count = 10000
	links := make([]domain.Link, count)
	for i := range links {
		links[i] = testLink(b, fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("%010d", i))
	}
	for _, mode := range []string{"ReadCode", "ReadOriginal", "SaveExisting", "Mixed95Read5Write"} {
		b.Run(mode, func(b *testing.B) {
			s := openStorage(b, count)
			ctx := context.Background()
			for _, l := range links {
				if _, err := s.Save(ctx, l); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					l := links[i%count]
					var got domain.Link
					var err error
					switch {
					case mode == "SaveExisting" || mode == "Mixed95Read5Write" && i%20 == 0:
						got, err = s.Save(ctx, l)
					case mode == "ReadOriginal":
						got, err = s.FindByOriginal(ctx, l.OriginalURL())
					default:
						got, err = s.FindByCode(ctx, l.Code())
					}
					if err != nil || got != l {
						b.Errorf("operation = %v, %v", got, err)
						return
					}
					i++
				}
			})
		})
	}
}

// Measures batches of 1000 new associations with concurrent readers/writers.
// Setup is excluded; map growth and allocations during insertion are included.
func BenchmarkStorageInsert(b *testing.B) {
	const count = 1000
	links := make([]domain.Link, count)
	for i := range links {
		links[i] = testLink(b, fmt.Sprintf("https://example.com/%d", i), fmt.Sprintf("%010d", i))
	}
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		s, err := NewStorage(context.Background(), Options{MaxEntries: count})
		if err != nil {
			b.Fatal(err)
		}
		var next atomic.Uint64
		b.StartTimer()
		// A fixed worker count keeps the measured workload independent of b.N.
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for {
					i := next.Add(1) - 1
					if i >= count {
						return
					}
					l := links[i]
					if _, err := s.Save(context.Background(), l); err != nil {
						b.Error(err)
						return
					}
					if _, err := s.FindByCode(context.Background(), l.Code()); err != nil {
						b.Error(err)
						return
					}
				}
			})
		}
		wg.Wait()
		b.StopTimer()
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
