package app

import (
	"context"
	"ozon-test-task/internal/shortener/domain"
	"path/filepath"
	"testing"
	"time"

	"ozon-test-task/internal/platform/config"
)

func TestResourcesRejectUnknownStorage(t *testing.T) {
	if _, err := newResources(context.Background(), config.Config{Storage: "unknown"}); err == nil {
		t.Fatal("unsupported storage accepted")
	}
}

func TestMemoryResourcesRecoverWithoutPostgres(t *testing.T) {
	cfg := config.Config{
		Storage:  "memory",
		Database: config.Database{URL: "invalid DSN", MaxConns: -1},
		Memory:   config.Memory{File: filepath.Join(t.TempDir(), "links.db"), CacheEntries: 1, MaxDiskBytes: 1 << 20, OperationTimeout: time.Second, BatchSize: 128, BatchDelay: time.Millisecond, MaxPendingWrites: 1024},
	}

	r, err := newResources(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})

	if r.postgres != nil || r.memory == nil || r.storage != r.memory {
		t.Fatal("incorrect memory dependencies")
	}

	original, err := domain.NewOriginalURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	code, err := domain.NewCode("a")
	if err != nil {
		t.Fatal(err)
	}

	link, err := domain.NewLink(original, code)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.storage.Save(context.Background(), link); err != nil {
		t.Fatal(err)
	}

	if err := r.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := newResources(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restored.Close(); err != nil {
			t.Error(err)
		}
	}()

	if got, err := restored.storage.FindByCode(context.Background(), code); err != nil || got != link {
		t.Fatalf("restored link = %v, %v", got, err)
	}
}
