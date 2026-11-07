package ports

import (
	"context"

	"ozon-test-task/internal/shortener/domain"
)

// CodeGenerator implementations must support concurrent calls. Generation
// alone does not guarantee uniqueness; Storage enforces it on insertion.
type CodeGenerator interface {
	Generate(context.Context) (domain.Code, error)
}
