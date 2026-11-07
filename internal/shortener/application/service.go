package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

const maxGenerationAttempts = 10

var ErrGenerationExhausted = errors.New("code generation attempts exhausted")

// Service is safe for concurrent use with implementations honoring the ports'
// concurrency contracts. It holds no mutable request state.
type Service struct {
	storage   ports.Storage
	generator ports.CodeGenerator
	timeout   time.Duration
}

func NewService(storage ports.Storage, generator ports.CodeGenerator, timeout time.Duration) (*Service, error) {
	if storage == nil || generator == nil || timeout <= 0 {
		return nil, errors.New("shortener requires storage, generator and a positive operation timeout")
	}
	return &Service{storage: storage, generator: generator, timeout: timeout}, nil
}

func (s *Service) Shorten(ctx context.Context, rawURL string) (domain.Link, error) {
	original, err := domain.NewOriginalURL(rawURL)
	if err != nil {
		return domain.Link{}, fmt.Errorf("shorten: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return domain.Link{}, fmt.Errorf("shorten: %w", err)
	}

	existing, err := s.storage.FindByOriginal(ctx, original)
	if err == nil {
		return existing, nil
	}

	if !errors.Is(err, ports.ErrNotFound) {
		return domain.Link{}, fmt.Errorf("find original: %w", err)
	}

	for range maxGenerationAttempts {
		if err := ctx.Err(); err != nil {
			return domain.Link{}, fmt.Errorf("shorten: %w", err)
		}

		code, err := s.generator.Generate(ctx)
		if err != nil {
			return domain.Link{}, fmt.Errorf("generate code: %w", err)
		}

		link, err := domain.NewLink(original, code)
		if err != nil {
			return domain.Link{}, fmt.Errorf("create link: %w", err)
		}

		saved, err := s.storage.Save(ctx, link)
		if err == nil {
			return saved, nil
		}

		if !errors.Is(err, ports.ErrCodeConflict) {
			return domain.Link{}, fmt.Errorf("save link: %w", err)
		}
	}

	return domain.Link{}, ErrGenerationExhausted
}

// Resolve returns the stored association; HTTP response behavior is left to a
// future transport adapter.
func (s *Service) Resolve(ctx context.Context, rawCode string) (domain.Link, error) {
	code, err := domain.NewCode(rawCode)
	if err != nil {
		return domain.Link{}, fmt.Errorf("resolve: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return domain.Link{}, fmt.Errorf("resolve: %w", err)
	}

	link, err := s.storage.FindByCode(ctx, code)
	if err != nil {
		return domain.Link{}, fmt.Errorf("find code: %w", err)
	}

	return link, nil
}
