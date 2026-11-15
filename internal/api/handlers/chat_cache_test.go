package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/cache"
	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
	"github.com/johnbykov1989/gatewayd/internal/routing"
)

// countingProvider — реальная реализация provider.Provider, которая считает вызовы.
// Не мок: реально отдаёт ChatResponse и Stream, реально инкрементирует счётчики.
type countingProvider struct {
	name        string
	mu          sync.Mutex
	doCalls     int
	streamCalls int
	resp        openai.ChatResponse
	chunks      []string
}

func (c *countingProvider) Name() string { return c.name }
func (c *countingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true, Streaming: true}
}
func (c *countingProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	c.mu.Lock()
	c.doCalls++
	c.mu.Unlock()
	return c.resp, nil
}
func (c *countingProvider) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	c.mu.Lock()
	c.streamCalls++
	c.mu.Unlock()
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for _, ch := range c.chunks {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, _ = pw.Write([]byte("data: " + ch + "\n\n"))
		}
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}
func (c *countingProvider) HealthCheck(_ context.Context) error      { return nil }
func (c *countingProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }
func (c *countingProvider) DoCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doCalls
}
func (c *countingProvider) StreamCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamCalls
}

func newCacheTestRouter(p *countingProvider) *routing.Router {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "test-model", Routes: []string{p.Name()}, Price: config.Price{Input: 0, Output: 0}},
		},
	}
	providers := map[string]provider.Provider{p.Name(): p}
	return routing.New(cfg, providers)
}

func newCacheTestHandler(t *testing.T, p *countingProvider, c *cache.Cache) *ChatHandler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := newCacheTestRouter(p)
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "test-model", Routes: []string{p.Name()}, Price: config.Price{Input: 0, Output: 0}},
		},
	}
	// m и tr — nil: в тестах кэша трассировка и метрики не проверяются.
	return NewChatCompletions(logger, router, nil, cfg, c, nil, nil)
}

func TestChatCompletions_Cache_NonStream(t *testing.T) {
	p := &countingProvider{
		name: "test-provider",
		resp: openai.ChatResponse{
			ID:     "chatcmpl-1",
			Object: "chat.completion",
			Model:  "test-model",
			Choices: []openai.Choice{
				{Index: 0, Message: openai.Message{Role: "assistant", Content: "Hello"}, FinishReason: "stop"},
			},
			Usage: &openai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	c := cache.New(time.Minute, 100)
	h := newCacheTestHandler(t, p, c)

	body := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}]}`

	// 1-й запрос: cache miss → провайдер вызван.
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	h.Handle(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200, body: %s", rr1.Code, rr1.Body.String())
	}
	if p.DoCalls() != 1 {
		t.Errorf("1st request: provider DoCalls = %d, want 1", p.DoCalls())
	}

	var resp1 struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rr1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("1st request: invalid JSON: %v", err)
	}
	if resp1.Choices[0].Message.Content != "Hello" {
		t.Errorf("1st request: content = %q, want Hello", resp1.Choices[0].Message.Content)
	}

	// 2-й запрос: cache hit → провайдер НЕ вызван.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.Handle(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("2nd request: status = %d, want 200", rr2.Code)
	}
	if p.DoCalls() != 1 {
		t.Errorf("2nd request: provider DoCalls = %d, want 1 (cache hit, provider not called)", p.DoCalls())
	}

	var resp2 struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("2nd request: invalid JSON: %v", err)
	}
	if resp2.Choices[0].Message.Content != "Hello" {
		t.Errorf("2nd request: content = %q, want Hello (cached)", resp2.Choices[0].Message.Content)
	}

	// Статистика кэша: 1 hit, 1 miss.
	stats := c.Stats()
	if stats.Hits != 1 {
		t.Errorf("cache Hits = %d, want 1", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("cache Misses = %d, want 1", stats.Misses)
	}
}

func TestChatCompletions_Cache_Stream(t *testing.T) {
	p := &countingProvider{
		name: "test-provider",
		chunks: []string{
			`{"choices":[{"delta":{"content":"A"}}]}`,
			`{"choices":[{"delta":{"content":"B"}}]}`,
			`[DONE]`,
		},
	}
	c := cache.New(time.Minute, 100)
	h := newCacheTestHandler(t, p, c)

	body := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}],"stream":true}`

	// 1-й запрос: cache miss → провайдер вызван.
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	h.Handle(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200", rr1.Code)
	}
	if p.StreamCalls() != 1 {
		t.Errorf("1st request: StreamCalls = %d, want 1", p.StreamCalls())
	}
	if !strings.Contains(rr1.Body.String(), "A") || !strings.Contains(rr1.Body.String(), "B") {
		t.Errorf("1st request: unexpected body: %s", rr1.Body.String())
	}

	// 2-й запрос: cache hit → провайдер НЕ вызван.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.Handle(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("2nd request: status = %d, want 200", rr2.Code)
	}
	if p.StreamCalls() != 1 {
		t.Errorf("2nd request: StreamCalls = %d, want 1 (cache hit)", p.StreamCalls())
	}
	if !strings.Contains(rr2.Body.String(), "A") || !strings.Contains(rr2.Body.String(), "B") {
		t.Errorf("2nd request: unexpected body: %s", rr2.Body.String())
	}

	stats := c.Stats()
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Errorf("cache stats = %+v, want hits=1 misses=1", stats)
	}
}

func TestChatCompletions_Cache_DifferentParamsMiss(t *testing.T) {
	p := &countingProvider{
		name: "test-provider",
		resp: openai.ChatResponse{ID: "1", Choices: []openai.Choice{{Message: openai.Message{Content: "X"}}}},
	}
	c := cache.New(time.Minute, 100)
	h := newCacheTestHandler(t, p, c)

	// Non-stream запрос.
	body1 := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}]}`
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	h.Handle(rr1, req1)

	// Stream запрос с тем же промптом — должен быть miss (разные paramsHash).
	body2 := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.Handle(rr2, req2)

	// Non-stream вызвал Do, stream вызвал Stream → 2 разных вызова.
	if p.DoCalls() != 1 {
		t.Errorf("DoCalls = %d, want 1", p.DoCalls())
	}
	if p.StreamCalls() != 1 {
		t.Errorf("StreamCalls = %d, want 1", p.StreamCalls())
	}
}

func TestChatCompletions_Cache_Disabled(t *testing.T) {
	// Кэш отключён (capacity 0) — каждый запрос идёт к провайдеру.
	p := &countingProvider{
		name: "test-provider",
		resp: openai.ChatResponse{ID: "1", Choices: []openai.Choice{{Message: openai.Message{Content: "X"}}}},
	}
	c := cache.New(time.Minute, 0)
	h := newCacheTestHandler(t, p, c)

	body := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}]}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.Handle(rr, req)
	}

	if p.DoCalls() != 3 {
		t.Errorf("DoCalls = %d, want 3 (cache disabled)", p.DoCalls())
	}
}

func TestChatCompletions_Cache_NilCache(t *testing.T) {
	// h.cache == nil — всё работает без паники, провайдер вызывается всегда.
	p := &countingProvider{
		name: "test-provider",
		resp: openai.ChatResponse{ID: "1", Choices: []openai.Choice{{Message: openai.Message{Content: "X"}}}},
	}
	h := newCacheTestHandler(t, p, nil)

	body := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if p.DoCalls() != 1 {
		t.Errorf("DoCalls = %d, want 1", p.DoCalls())
	}
}
