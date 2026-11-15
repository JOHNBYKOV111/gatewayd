package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
	"github.com/johnbykov1989/gatewayd/internal/provider/ollama"
	"github.com/johnbykov1989/gatewayd/internal/routing"
	"github.com/johnbykov1989/gatewayd/internal/transport"
)

func TestChatCompletions_Integration(t *testing.T) {
	fakeOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-123",
			"object": "chat.completion",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "Hello from integration test"}, "finish_reason": "stop"},
			},
		})
	}))
	defer fakeOllama.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := transport.NewClient(transport.DefaultTimeouts())

	cfg := &config.Config{
		Models: []config.Model{
			{Name: "test-model", Routes: []string{"fake-ollama"}},
		},
	}

	providers := map[string]provider.Provider{
		"fake-ollama": ollama.New("fake-ollama", fakeOllama.URL, "", client),
	}

	router := routing.New(cfg, providers)

	body := `{"model":"test-model","messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler := NewChatCompletions(logger, router, nil, nil, nil, nil, nil).Handle
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Hello from integration test" {
		t.Errorf("unexpected response: %s", rr.Body.String())
	}
}

type slowProvider struct {
	name string
}

func (s *slowProvider) Name() string { return s.name }
func (s *slowProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true, Streaming: true}
}
func (s *slowProvider) Do(ctx context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	select {
	case <-ctx.Done():
		return openai.ChatResponse{}, provider.NormalizeError(ctx.Err())
	case <-time.After(100 * time.Millisecond):
		return openai.ChatResponse{ID: "slow-resp"}, nil
	}
}
func (s *slowProvider) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			_, _ = pw.Write([]byte("data: chunk\n\n"))
		}
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}
func (s *slowProvider) HealthCheck(_ context.Context) error      { return nil }
func (s *slowProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }

func TestGracefulShutdown_InFlightRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		Models: []config.Model{
			{Name: "slow-model", Routes: []string{"slow"}},
		},
	}
	prov := &slowProvider{name: "slow"}
	router := routing.New(cfg, map[string]provider.Provider{"slow": prov})

	handler := NewChatCompletions(logger, router, nil, nil, nil, nil, nil).Handle

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	body := `{"model":"slow-model","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish within 2s after context cancellation (graceful shutdown failed)")
	}
}

func TestGracefulShutdown_StreamingRequest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		Models: []config.Model{
			{Name: "slow-model", Routes: []string{"slow"}},
		},
	}
	prov := &slowProvider{name: "slow"}
	router := routing.New(cfg, map[string]provider.Provider{"slow": prov})

	handler := NewChatCompletions(logger, router, nil, nil, nil, nil, nil).Handle
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	body := `{"model":"slow-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streaming handler did not finish within 2s after context cancellation (graceful shutdown failed)")
	}
}
