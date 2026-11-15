package provider

import (
	"context"
	"errors"
	"net/http"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
)

// Capabilities описывает, что провайдер умеет.
type Capabilities struct {
	Chat       bool
	Streaming  bool
	Embeddings bool
	Tools      bool
	Vision     bool
}

// Result — канонический результат выполнения запроса.
// Он инкапсулирует логику ответа: JSON или SSE.
type Result interface {
	Write(w http.ResponseWriter) error
}

// Provider — единый интерфейс для всех LLM-бэкендов.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Do(ctx context.Context, req openai.ChatRequest) (openai.ChatResponse, error)
	Stream(ctx context.Context, req openai.ChatRequest) (stream.Stream, error)
	HealthCheck(ctx context.Context) error
	Usage(resp openai.ChatResponse) openai.Usage
}

// Ошибки провайдеров — единые коды для роутера и fallback-логики.
var (
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrRateLimited         = errors.New("provider rate limited")
	ErrUnauthorized        = errors.New("provider unauthorized")
	ErrInvalidRequest      = errors.New("provider invalid request")
	ErrTimeout             = errors.New("provider timeout")
)

func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	case errors.Is(err, context.Canceled):
		return err
	}
	return err
}
