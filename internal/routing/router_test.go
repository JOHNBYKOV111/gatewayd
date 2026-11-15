package routing

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

type fakeProvider struct {
	name string
}

func (f *fakeProvider) Name() string                        { return f.name }
func (f *fakeProvider) Capabilities() provider.Capabilities { return provider.Capabilities{Chat: true} }
func (f *fakeProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	return openai.ChatResponse{ID: "fake-resp"}, nil
}
func (f *fakeProvider) Stream(_ context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	return nil, nil
}
func (f *fakeProvider) HealthCheck(_ context.Context) error      { return nil }
func (f *fakeProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }

type failProvider struct{ name string }

func (f *failProvider) Name() string                        { return f.name }
func (f *failProvider) Capabilities() provider.Capabilities { return provider.Capabilities{Chat: true} }
func (f *failProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	return openai.ChatResponse{}, provider.ErrProviderUnavailable
}
func (f *failProvider) Stream(_ context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	return nil, provider.ErrProviderUnavailable
}
func (f *failProvider) HealthCheck(_ context.Context) error      { return nil }
func (f *failProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }

type recordingProvider struct {
	name string
	mu   sync.Mutex
	hits int
}

func (r *recordingProvider) Name() string { return r.name }
func (r *recordingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true}
}
func (r *recordingProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	r.mu.Lock()
	r.hits++
	r.mu.Unlock()
	return openai.ChatResponse{ID: "rec-" + r.name}, nil
}
func (r *recordingProvider) Stream(_ context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	return nil, nil
}
func (r *recordingProvider) HealthCheck(_ context.Context) error      { return nil }
func (r *recordingProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }
func (r *recordingProvider) Hints() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

// finiteStreamProvider отдаёт N чанков и завершается с io.EOF.
// Используется для тестов breakerStream.
type finiteStreamProvider struct {
	name   string
	chunks int
}

func (f *finiteStreamProvider) Name() string { return f.name }
func (f *finiteStreamProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true, Streaming: true}
}
func (f *finiteStreamProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	return openai.ChatResponse{}, provider.ErrProviderUnavailable
}
func (f *finiteStreamProvider) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for i := 0; i < f.chunks; i++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, _ = pw.Write([]byte("data: chunk\n\n"))
		}
		// Нормальное завершение потока (без ошибки).
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}
func (f *finiteStreamProvider) HealthCheck(_ context.Context) error      { return nil }
func (f *finiteStreamProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }

func TestRouter_Resolve_Success(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "test-model", Routes: []string{"test-provider"}},
		},
	}
	providers := map[string]provider.Provider{
		"test-provider": &fakeProvider{name: "test-provider"},
	}

	r := New(cfg, providers)
	resp, providerName, err := r.Do(context.Background(), openai.ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.ID != "fake-resp" {
		t.Errorf("expected fake-resp, got %s", resp.ID)
	}
	if providerName != "test-provider" {
		t.Errorf("providerName = %q, want test-provider", providerName)
	}
}

func TestRouter_Resolve_ModelNotFound(t *testing.T) {
	cfg := &config.Config{}
	providers := map[string]provider.Provider{}
	r := New(cfg, providers)

	_, _, err := r.Do(context.Background(), openai.ChatRequest{Model: "unknown-model"})
	if err == nil {
		t.Fatal("expected error for unknown model, got nil")
	}
	if !strings.Contains(err.Error(), "unknown-model") {
		t.Errorf("expected error to mention unknown-model, got: %v", err)
	}
}

func TestRouter_Fallback(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "resilient", Routes: []string{"failing", "healthy"}},
		},
	}
	providers := map[string]provider.Provider{
		"failing": &failProvider{name: "failing"},
		"healthy": &fakeProvider{name: "healthy"},
	}
	r := New(cfg, providers)

	resp, providerName, err := r.Do(context.Background(), openai.ChatRequest{Model: "resilient"})
	if err != nil {
		t.Fatalf("fallback should succeed: %v", err)
	}
	if resp.ID != "fake-resp" {
		t.Errorf("fallback routed to %q, want fake-resp", resp.ID)
	}
	if providerName != "healthy" {
		t.Errorf("providerName = %q, want healthy", providerName)
	}
}

func TestRouter_FallbackAllFail(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "doomed", Routes: []string{"fail1", "fail2"}},
		},
	}
	providers := map[string]provider.Provider{
		"fail1": &failProvider{name: "fail1"},
		"fail2": &failProvider{name: "fail2"},
	}
	r := New(cfg, providers)

	_, _, err := r.Do(context.Background(), openai.ChatRequest{Model: "doomed"})
	if !errors.Is(err, provider.ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
}

func TestRouter_Counter(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "counted", Routes: []string{"counter"}},
		},
	}
	prov := &recordingProvider{name: "counter"}
	providers := map[string]provider.Provider{"counter": prov}
	r := New(cfg, providers)

	for i := 0; i < 3; i++ {
		_, _, _ = r.Do(context.Background(), openai.ChatRequest{Model: "counted"})
	}

	if got := r.RequestsTotal(); got != 3 {
		t.Errorf("RequestsTotal = %d, want 3", got)
	}
}

func TestRouter_Models(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "model-a", Routes: []string{"p"}},
			{Name: "model-b", Routes: []string{"p"}},
		},
	}
	r := New(cfg, map[string]provider.Provider{})

	models := r.Models()
	if len(models) != 2 {
		t.Fatalf("Models() = %d, want 2", len(models))
	}
}

func TestBreakerStream_EOFRecordsSuccess(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "streaming", Routes: []string{"finite"}},
		},
	}
	prov := &finiteStreamProvider{name: "finite", chunks: 2}
	r := New(cfg, map[string]provider.Provider{"finite": prov})

	st, providerName, err := r.Stream(context.Background(), openai.ChatRequest{Model: "streaming"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if providerName != "finite" {
		t.Errorf("providerName = %q, want finite", providerName)
	}
	for {
		if _, err := st.Next(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
	}
	_ = st.Close()

	if state, _ := r.BreakerState("finite"); state != StateClosed {
		t.Errorf("breaker state = %v, want closed after io.EOF", state)
	}
}

func TestBreakerStream_CloseBeforeEOFRecordsSuccess(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "streaming", Routes: []string{"finite"}},
		},
	}
	prov := &finiteStreamProvider{name: "finite", chunks: 100}
	r := New(cfg, map[string]provider.Provider{"finite": prov})

	st, _, err := r.Stream(context.Background(), openai.ChatRequest{Model: "streaming"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _ = st.Next()
	_ = st.Close()

	if state, _ := r.BreakerState("finite"); state != StateClosed {
		t.Errorf("breaker state = %v, want closed after client Close()", state)
	}
}
