package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ozon-test-task/internal/shortener/domain"
)

func TestNewStorageValidation(t *testing.T) {
	for _, tt := range []struct {
		pool    *pgxpool.Pool
		timeout time.Duration
	}{
		{nil, time.Second}, {&pgxpool.Pool{}, 0}, {&pgxpool.Pool{}, -time.Second},
	} {
		if _, err := NewStorage(tt.pool, tt.timeout); err == nil {
			t.Fatal("invalid dependencies accepted")
		}
	}
}

func TestSaveRejectsZeroLink(t *testing.T) {
	s := &Storage{timeout: time.Second}
	if _, err := s.Save(context.Background(), domain.Link{}); !errors.Is(err, domain.ErrInvalidOriginalURL) {
		t.Fatal(err)
	}
}

func TestStorageErrorsHideSensitiveData(t *testing.T) {
	const secret = "https://example.com/private?token=sensitive" // #nosec G101 -- test payload verifies that sensitive input is removed.
	for _, original := range []error{
		&pgconn.PgError{Code: "23505", Message: secret, Detail: secret}, errors.New(secret),
	} {
		err := storageError(context.Background(), "save", original)
		if strings.Contains(err.Error(), secret) {
			t.Fatal("error exposes input")
		}
	}
	for _, original := range []error{context.Canceled, context.DeadlineExceeded} {
		if err := storageError(context.Background(), "find", original); !errors.Is(err, original) {
			t.Fatal(err)
		}
	}
}
