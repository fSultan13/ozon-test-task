package random

import (
	"context"
	"crypto/rand"
	"fmt"

	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

// Generator uses independent local buffers and crypto/rand's concurrent-safe
// source. It has no counter to coordinate across processes or restarts.
type Generator struct{}

var _ ports.CodeGenerator = Generator{}

func (Generator) Generate(ctx context.Context) (domain.Code, error) {
	var code [domain.MaxCodeLength]byte
	var entropy [16]byte
	for n := 0; n < len(code); {
		if err := ctx.Err(); err != nil {
			return domain.Code{}, fmt.Errorf("random code: %w", err)
		}

		if _, err := rand.Read(entropy[:]); err != nil {
			return domain.Code{}, fmt.Errorf("read entropy: %w", err)
		}

		for _, b := range entropy {
			// 252 is the largest multiple of 63 below 256. Rejecting the
			// remaining bytes avoids bias from the modulo operation.
			if int(b) >= 256-256%len(domain.CodeAlphabet) {
				continue
			}
			code[n] = domain.CodeAlphabet[int(b)%len(domain.CodeAlphabet)]
			n++
			if n == len(code) {
				break
			}
		}
	}
	return domain.NewCode(string(code[:]))
}
