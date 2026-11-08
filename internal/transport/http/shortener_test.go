package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	shortenerhttp "ozon-test-task/internal/shortener/adapters/http"
	"ozon-test-task/internal/shortener/adapters/memory"
	"ozon-test-task/internal/shortener/adapters/random"
	"ozon-test-task/internal/shortener/application"
)

func TestShortenerRoundTripConcurrent(t *testing.T) {
	storage, err := memory.NewStorage(t.Context(), memory.Options{MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storage.Close(); err != nil {
			t.Error(err)
		}
	})
	service, err := application.NewService(storage, random.Generator{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	router := NewRouter(slog.New(slog.NewJSONHandler(&logs, nil)), nil, 1024, shortenerhttp.NewHandler(service, "https://short.example").RegisterRoutes)
	const original = "https://example.com/a%2Fb?q=%2f#secret"
	const workers = 32
	results := make(chan string, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			req := httptest.NewRequest("POST", "/links", strings.NewReader(`{"url":"`+original+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rw := httptest.NewRecorder()
			router.ServeHTTP(rw, req)
			var response shortenerhttp.ShortenResponse
			if rw.Code != 200 || json.Unmarshal(rw.Body.Bytes(), &response) != nil {
				t.Errorf("POST = %d %s", rw.Code, rw.Body)
				return
			}
			results <- response.ShortURL
		})
	}
	wg.Wait()
	close(results)
	var shortURL string
	for result := range results {
		if shortURL != "" && result != shortURL {
			t.Fatal("concurrent POST returned different links")
		}
		shortURL = result
	}
	code := strings.TrimPrefix(shortURL, "https://short.example/links/")
	if len(code) != 10 {
		t.Fatalf("invalid short URL %q", shortURL)
	}
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, httptest.NewRequest("GET", "/links/"+code, nil))
	var response shortenerhttp.ResolveResponse
	if rw.Code != 200 || json.Unmarshal(rw.Body.Bytes(), &response) != nil || response.URL != original {
		t.Fatalf("GET = %d %s", rw.Code, rw.Body)
	}
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/links/unknown", "", 404},
		{"GET", "/links/bad-code", "", 400},
		{"DELETE", "/links/" + code, "", 405},
		{"POST", "/links", `{"url":"https://other.example"}`, 503},
		{"POST", "/links", `{"url":"invalid"}`, 400},
	} {
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		if rw.Code != tt.status {
			t.Errorf("%s %s = %d", tt.method, tt.path, rw.Code)
		}
	}
	if strings.Contains(logs.String(), original) || strings.Contains(logs.String(), code) {
		t.Fatal("URLs leaked in logs")
	}
}

func BenchmarkShortenerHTTP(b *testing.B) {
	storage, err := memory.NewStorage(b.Context(), memory.Options{MaxEntries: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := storage.Close(); err != nil {
			b.Error(err)
		}
	})
	service, err := application.NewService(storage, random.Generator{}, time.Second)
	if err != nil {
		b.Fatal(err)
	}
	link, err := service.Shorten(b.Context(), "https://example.com")
	if err != nil {
		b.Fatal(err)
	}
	router := NewRouter(slog.New(slog.DiscardHandler), nil, 1024, shortenerhttp.NewHandler(service, "http://localhost:8080").RegisterRoutes)
	for _, method := range []string{"GET", "POST"} {
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					path := "/links"
					if method == "GET" {
						path += "/" + link.Code().String()
					}
					req := httptest.NewRequest(method, path, strings.NewReader(`{"url":"https://example.com"}`))
					req.Header.Set("Content-Type", "application/json")
					rw := httptest.NewRecorder()
					router.ServeHTTP(rw, req)
					if rw.Code != 200 {
						b.Errorf("status = %d", rw.Code)
					}
				}
			})
		})
	}
}
