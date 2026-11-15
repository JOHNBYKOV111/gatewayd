package routing

import (
	"context"
	"sync"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// alwaysFailProvider — фейк, который всегда возвращает ошибку и считает вызовы.
type alwaysFailProvider struct {
	name string
	mu   sync.Mutex
	hits int
}

func (f *alwaysFailProvider) Name() string { return f.name }
func (f *alwaysFailProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true}
}
func (f *alwaysFailProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()
	return openai.ChatResponse{}, provider.ErrProviderUnavailable
}
func (f *alwaysFailProvider) Stream(_ context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()
	return nil, provider.ErrProviderUnavailable
}
func (f *alwaysFailProvider) HealthCheck(_ context.Context) error      { return nil }
func (f *alwaysFailProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }
func (f *alwaysFailProvider) Hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func TestRouter_BreakerOpensAfterFailures(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "unstable", Routes: []string{"bad"}},
		},
	}
	bad := &alwaysFailProvider{name: "bad"}
	providers := map[string]provider.Provider{"bad": bad}
	r := New(cfg, providers)

	// 3 запроса, все падают. После 3-го breaker должен открыться (порог 3).
	for i := 0; i < 3; i++ {
		_, _, _ = r.Do(context.Background(), openai.ChatRequest{Model: "unstable"})
	}

	if state, ok := r.BreakerState("bad"); !ok || state != StateOpen {
		t.Fatalf("breaker state = %v, want open", state)
	}

	// 4-й запрос должен вернуть ErrProviderUnavailable БЕЗ вызова провайдера.
	before := bad.Hits()
	_, _, err := r.Do(context.Background(), openai.ChatRequest{Model: "unstable"})
	if err == nil {
		t.Fatal("expected error from open breaker, got nil")
	}
	if bad.Hits() != before {
		t.Errorf("provider hits = %d, want %d (breaker should not call provider when open)", bad.Hits(), before)
	}
}

func TestRouter_BreakerFallbackAfterOpen(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "resilient", Routes: []string{"bad", "good"}},
		},
	}
	bad := &alwaysFailProvider{name: "bad"}
	good := &fakeProvider{name: "good"}
	providers := map[string]provider.Provider{
		"bad":  bad,
		"good": good,
	}
	r := New(cfg, providers)

	// Первые 3 запроса: bad падает 3 раза, breaker открывается, но fallback на good.
	for i := 0; i < 3; i++ {
		resp, providerName, err := r.Do(context.Background(), openai.ChatRequest{Model: "resilient"})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if resp.ID != "fake-resp" {
			t.Errorf("request %d: resp.ID = %q, want fake-resp", i, resp.ID)
		}
		if providerName != "good" {
			t.Errorf("request %d: providerName = %q, want good", i, providerName)
		}
	}

	// 4-й запрос: bad в open, роутер идёт сразу в good.
	before := bad.Hits()
	resp, providerName, err := r.Do(context.Background(), openai.ChatRequest{Model: "resilient"})
	if err != nil {
		t.Fatalf("request 4: %v", err)
	}
	if resp.ID != "fake-resp" {
		t.Errorf("request 4: resp.ID = %q, want fake-resp", resp.ID)
	}
	if providerName != "good" {
		t.Errorf("request 4: providerName = %q, want good", providerName)
	}
	if bad.Hits() != before {
		t.Errorf("bad provider hits changed from %d to %d; breaker should have skipped it", before, bad.Hits())
	}
}

func TestRouter_BreakerState_UnknownProvider(t *testing.T) {
	cfg := &config.Config{}
	r := New(cfg, map[string]provider.Provider{})

	_, ok := r.BreakerState("nonexistent")
	if ok {
		t.Error("BreakerState for unknown provider should return false")
	}
}
