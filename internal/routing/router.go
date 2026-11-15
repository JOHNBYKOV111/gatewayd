package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/api/response"
	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

type Router struct {
	providers map[string]provider.Provider
	models    map[string][]string // model name → список провайдеров (fallback-цепочка)
	breakers  map[string]*Breaker // provider name → circuit breaker
	requests  atomic.Int64        // счётчик gw_requests_total, задел под П36
	mu        sync.RWMutex        // на случай будущих изменений конфига в рантайме
}

func New(cfg *config.Config, providers map[string]provider.Provider) *Router {
	models := make(map[string][]string, len(cfg.Models))
	for _, m := range cfg.Models {
		models[m.Name] = m.Routes
	}

	breakers := make(map[string]*Breaker, len(providers))
	for name := range providers {
		// Порог 3 ошибки, таймаут 30 секунд — можно вынести в конфиг позже.
		breakers[name] = NewBreaker(3, 30*time.Second)
	}

	return &Router{
		providers: providers,
		models:    models,
		breakers:  breakers,
	}
}

func (r *Router) resolve(model string) ([]provider.Provider, []*Breaker, error) {
	r.mu.RLock()
	routes, ok := r.models[model]
	r.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("routing: model %q not configured", model)
	}

	providers := make([]provider.Provider, 0, len(routes))
	breakers := make([]*Breaker, 0, len(routes))
	for _, name := range routes {
		p, ok := r.providers[name]
		if !ok {
			return nil, nil, fmt.Errorf("routing: provider %q not found", name)
		}
		providers = append(providers, p)
		breakers = append(breakers, r.breakers[name])
	}
	return providers, breakers, nil
}

// Do выполняет нестриминговый запрос и возвращает ответ вместе с именем провайдера,
// который реально его обработал (для журнала).
func (r *Router) Do(ctx context.Context, req openai.ChatRequest) (openai.ChatResponse, string, error) {
	r.requests.Add(1)

	providers, breakers, err := r.resolve(req.Model)
	if err != nil {
		return openai.ChatResponse{}, "", err
	}

	var lastErr error
	for i, p := range providers {
		b := breakers[i]
		if !b.Allow() {
			lastErr = provider.ErrProviderUnavailable
			continue
		}

		resp, err := p.Do(ctx, req)
		if err == nil {
			b.RecordSuccess()
			return resp, p.Name(), nil
		}
		b.RecordFailure()
		lastErr = err
	}
	return openai.ChatResponse{}, "", lastErr
}

// Stream выполняет стриминговый запрос и возвращает поток вместе с именем провайдера.
func (r *Router) Stream(ctx context.Context, req openai.ChatRequest) (stream.Stream, string, error) {
	r.requests.Add(1)

	providers, breakers, err := r.resolve(req.Model)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for i, p := range providers {
		b := breakers[i]
		if !b.Allow() {
			lastErr = provider.ErrProviderUnavailable
			continue
		}

		st, err := p.Stream(ctx, req)
		if err != nil {
			b.RecordFailure()
			lastErr = err
			continue
		}

		// Не записываем успех сразу — поток может оборваться на первом же чанке.
		// Оборачиваем в breakerStream, который вызовет RecordSuccess на io.EOF
		// и RecordFailure на любом другом обрыве.
		return &breakerStream{inner: st, breaker: b}, p.Name(), nil
	}
	return nil, "", lastErr
}

// breakerStream оборачивает stream.Stream и уведомляет breaker о реальном
// результате чтения потока: io.EOF — успех, любая другая ошибка — провал.
type breakerStream struct {
	inner   stream.Stream
	breaker *Breaker
	done    bool
}

func (s *breakerStream) Next() (stream.Event, error) {
	if s.done {
		return stream.Event{}, io.EOF
	}
	if s.inner == nil {
		s.done = true
		return stream.Event{}, io.EOF
	}

	ev, err := s.inner.Next()
	if err != nil {
		s.done = true
		if errors.Is(err, io.EOF) {
			s.breaker.RecordSuccess()
		} else {
			s.breaker.RecordFailure()
		}
		return ev, err
	}
	return ev, nil
}

func (s *breakerStream) Close() error {
	if !s.done {
		s.done = true
		// Если закрыли до io.EOF — считаем это успехом (клиент ушёл, не из-за провайдера).
		s.breaker.RecordSuccess()
	}
	if s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

// Execute — единая точка входа для хендлера.
// Возвращает response.Result с информацией о провайдере и usage.
func (r *Router) Execute(ctx context.Context, req openai.ChatRequest, logger *slog.Logger) (response.Result, error) {
	if req.Stream {
		st, providerName, err := r.Stream(ctx, req)
		if err != nil {
			return nil, err
		}
		return response.NewStreamResult(st, logger, providerName), nil
	}

	resp, providerName, err := r.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	return response.NewJSONResult(resp, providerName), nil
}

// RequestsTotal возвращает количество обработанных запросов.
func (r *Router) RequestsTotal() int64 {
	return r.requests.Load()
}

// Models возвращает список моделей из конфига (для GET /v1/models).
func (r *Router) Models() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.models))
	for name := range r.models {
		out = append(out, name)
	}
	return out
}

// BreakerState возвращает состояние breaker для провайдера (для метрик П36).
func (r *Router) BreakerState(providerName string) (State, bool) {
	b, ok := r.breakers[providerName]
	if !ok {
		return StateClosed, false
	}
	return b.State(), true
}
