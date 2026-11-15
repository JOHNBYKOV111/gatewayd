package routing

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// flakyStreamProvider — фейк, который отдаёт N чанков, потом рвёт поток.
type flakyStreamProvider struct {
	name   string
	chunks int
	mu     sync.Mutex
	hits   int
}

func (f *flakyStreamProvider) Name() string { return f.name }
func (f *flakyStreamProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: true, Streaming: true}
}
func (f *flakyStreamProvider) Do(_ context.Context, _ openai.ChatRequest) (openai.ChatResponse, error) {
	return openai.ChatResponse{}, provider.ErrProviderUnavailable
}
func (f *flakyStreamProvider) Stream(ctx context.Context, _ openai.ChatRequest) (stream.Stream, error) {
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()

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
		_ = pw.CloseWithError(errors.New("fake: stream aborted"))
	}()
	return stream.NewReaderWithContext(ctx, pr), nil
}
func (f *flakyStreamProvider) HealthCheck(_ context.Context) error      { return nil }
func (f *flakyStreamProvider) Usage(_ openai.ChatResponse) openai.Usage { return openai.Usage{} }
func (f *flakyStreamProvider) Hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func TestScenario_StreamAbortThenBreakerFallback(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "combo", Routes: []string{"flaky", "stable"}},
		},
	}
	flaky := &flakyStreamProvider{name: "flaky", chunks: 2}
	stable := &fakeProvider{name: "stable"}
	providers := map[string]provider.Provider{
		"flaky":  flaky,
		"stable": stable,
	}
	r := New(cfg, providers)

	for i := 0; i < 3; i++ {
		st, _, err := r.Stream(context.Background(), openai.ChatRequest{Model: "combo"})
		if err != nil {
			t.Fatalf("request %d: Stream: %v", i, err)
		}
		for {
			if _, err := st.Next(); err != nil {
				break
			}
		}
		_ = st.Close()
	}

	if state, ok := r.BreakerState("flaky"); !ok || state != StateOpen {
		t.Fatalf("flaky breaker state = %v, want open", state)
	}

	before := flaky.Hits()
	st, providerName, err := r.Stream(context.Background(), openai.ChatRequest{Model: "combo"})
	if err != nil {
		t.Fatalf("request 4: Stream: %v", err)
	}
	if providerName != "stable" {
		t.Errorf("providerName = %q, want stable", providerName)
	}
	for {
		if _, err := st.Next(); err != nil {
			break
		}
	}
	_ = st.Close()
	if flaky.Hits() != before {
		t.Errorf("flaky.Hits = %d, want %d (breaker should have skipped it)", flaky.Hits(), before)
	}
}

func TestScenario_CounterCountsAllRequests(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "any", Routes: []string{"fail", "ok"}},
		},
	}
	providers := map[string]provider.Provider{
		"fail": &failProvider{name: "fail"},
		"ok":   &fakeProvider{name: "ok"},
	}
	r := New(cfg, providers)

	for i := 0; i < 5; i++ {
		_, _, _ = r.Do(context.Background(), openai.ChatRequest{Model: "any"})
	}

	if got := r.RequestsTotal(); got != 5 {
		t.Errorf("RequestsTotal = %d, want 5", got)
	}
}

func TestScenario_AllProvidersBreakersOpen(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "dead", Routes: []string{"fail1", "fail2"}},
		},
	}
	providers := map[string]provider.Provider{
		"fail1": &failProvider{name: "fail1"},
		"fail2": &failProvider{name: "fail2"},
	}
	r := New(cfg, providers)

	for i := 0; i < 3; i++ {
		_, _, _ = r.Do(context.Background(), openai.ChatRequest{Model: "dead"})
	}
	if s1, _ := r.BreakerState("fail1"); s1 != StateOpen {
		t.Errorf("fail1 breaker = %v, want open", s1)
	}
	if s2, _ := r.BreakerState("fail2"); s2 != StateOpen {
		t.Errorf("fail2 breaker = %v, want open", s2)
	}

	_, _, err := r.Do(context.Background(), openai.ChatRequest{Model: "dead"})
	if !errors.Is(err, provider.ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got %v", err)
	}
	if got := r.RequestsTotal(); got != 4 {
		t.Errorf("RequestsTotal = %d, want 4", got)
	}
}

func TestScenario_StreamInterruptedByClient(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "streaming", Routes: []string{"flaky"}},
		},
	}
	flaky := &flakyStreamProvider{name: "flaky", chunks: 100}
	providers := map[string]provider.Provider{"flaky": flaky}
	r := New(cfg, providers)

	ctx, cancel := context.WithCancel(context.Background())
	st, _, err := r.Stream(ctx, openai.ChatRequest{Model: "streaming"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _ = st.Next()
	cancel()
	_ = st.Close()

	if got := r.RequestsTotal(); got != 1 {
		t.Errorf("RequestsTotal = %d, want 1", got)
	}
}
