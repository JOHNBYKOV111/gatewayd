package handlers

import (
	"context"
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
	"github.com/johnbykov1989/gatewayd/internal/routing"
)

// fakeStreamProvider — это не мок, а реальная реализация интерфейса provider.Provider
type fakeStreamProvider struct {
	chunks []string
	delay  time.Duration
}

func (f *fakeStreamProvider) Name() string { return "fake-stream" }
func (f *fakeStreamProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Streaming: true}
}
func (f *fakeStreamProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	return openai.ChatResponse{}, nil
}
func (f *fakeStreamProvider) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for _, c := range f.chunks {
			if f.delay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(f.delay):
				}
			}
			if _, err := pw.Write([]byte("data: " + c + "\n\n")); err != nil {
				return
			}
		}
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}
func (f *fakeStreamProvider) HealthCheck(_ context.Context) error { return nil }
func (f *fakeStreamProvider) Usage(_ openai.ChatResponse) openai.Usage {
	return openai.Usage{}
}

func newStreamRouter(chunks []string) *routing.Router {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "stream-model", Routes: []string{"fake-stream"}},
		},
	}
	providers := map[string]provider.Provider{
		"fake-stream": &fakeStreamProvider{chunks: chunks},
	}
	return routing.New(cfg, providers)
}

func TestChatCompletions_Stream(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := newStreamRouter([]string{
		`{"choices":[{"delta":{"content":"A"}}]}`,
		`{"choices":[{"delta":{"content":"B"}}]}`,
	})

	body := `{"model":"stream-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	NewChatCompletions(logger, router, nil, nil, nil, nil, nil).Handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	out := rr.Body.String()
	if !strings.Contains(out, `"content":"A"`) {
		t.Errorf("missing chunk A:\n%s", out)
	}
	if !strings.Contains(out, `"content":"B"`) {
		t.Errorf("missing chunk B:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("missing [DONE]:\n%s", out)
	}
}

func TestChatCompletions_StreamClientDisconnect(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		Models: []config.Model{
			{Name: "stream-model", Routes: []string{"slow-provider"}},
		},
	}
	providers := map[string]provider.Provider{
		"slow-provider": &fakeStreamProvider{
			chunks: []string{`{"a":1}`, `{"b":2}`, `{"c":3}`, `{"d":4}`},
			delay:  50 * time.Millisecond,
		},
	}
	router := routing.New(cfg, providers)

	body := `{"model":"stream-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewChatCompletions(logger, router, nil, nil, nil, nil, nil).Handle(rr, req)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation (hung)")
	}
}
