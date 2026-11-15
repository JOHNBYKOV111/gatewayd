package handlers

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/johnbykov1989/gatewayd/internal/api/middleware"
	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/provider"
	"github.com/johnbykov1989/gatewayd/internal/routing"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestChatCompletions_ContentType(t *testing.T) {
	logger := newTestLogger()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, nil, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "content-type must be application/json") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	logger := newTestLogger()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, nil, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestChatCompletions_BodyTooLarge(t *testing.T) {
	logger := newTestLogger()
	large := bytes.Repeat([]byte("a"), 11<<20)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(large))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, nil, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestChatCompletions_MissingModel(t *testing.T) {
	logger := newTestLogger()
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, nil, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "model is required") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestChatCompletions_EmptyMessages(t *testing.T) {
	logger := newTestLogger()
	body := `{"model":"gpt-4o","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, nil, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "messages must not be empty") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

// TestChatCompletions_Tracing_Attributes проверяет, что на root-span
// устанавливается атрибут model, как требует ТЗ П38.
// Используем реальный in-memory TracerProvider (стандартный OTel-инструмент),
// а также рабочий routing.Router с fake-провайдером, чтобы Execute не падал.
func TestChatCompletions_Tracing_Attributes(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tr := tracing.New(tp.Tracer("gatewayd-test"))

	logger := newTestLogger()

	// Создаём реальный роутер с countingProvider из chat_cache_test.go.
	p := &countingProvider{
		name: "test-provider",
		resp: openai.ChatResponse{
			ID:     "chatcmpl-1",
			Object: "chat.completion",
			Model:  "llama3.2:3b",
			Choices: []openai.Choice{
				{Index: 0, Message: openai.Message{Role: "assistant", Content: "Hello"}, FinishReason: "stop"},
			},
		},
	}
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "llama3.2:3b", Routes: []string{p.Name()}, Price: config.Price{Input: 0, Output: 0}},
		},
	}
	providers := map[string]provider.Provider{p.Name(): p}
	router := routing.New(cfg, providers)

	handler := NewChatCompletions(logger, router, nil, cfg, nil, nil, tr)

	// Оборачиваем в Tracing-middleware, чтобы root-span создался.
	mw := middleware.Tracing(tr)(http.HandlerFunc(handler.Handle))

	body := `{"model":"llama3.2:3b","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("ожидался хотя бы 1 span (root)")
	}

	var foundRoot bool
	for _, s := range spans {
		if s.Name != "http.request" {
			continue
		}
		foundRoot = true
		attrs := make(map[string]string)
		for _, a := range s.Attributes {
			attrs[string(a.Key)] = a.Value.String()
		}
		if attrs["model"] != "llama3.2:3b" {
			t.Errorf("атрибут model: %q, ожидалось %q", attrs["model"], "llama3.2:3b")
		}
	}
	if !foundRoot {
		t.Error("root-span 'http.request' не найден")
	}
}
