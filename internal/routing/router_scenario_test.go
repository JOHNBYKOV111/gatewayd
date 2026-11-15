package routing

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// TestRouter_Scenario_OllamaThenAnthropic — первый (Ollama) падает, второй (Anthropic) отвечает.
func TestRouter_Scenario_OllamaThenAnthropic(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "gpt-4o", Routes: []string{"ollama", "anthropic", "fake"}},
		},
	}
	ollama := &fakeProviderControls{
		name: "ollama",
		err:  provider.ErrProviderUnavailable,
	}
	anthropic := &fakeProviderControls{
		name: "anthropic",
		resp: openai.ChatResponse{ID: "anth-1", Model: "claude-3-5-sonnet"},
	}
	fake := &fakeProviderControls{
		name: "fake",
		resp: openai.ChatResponse{ID: "fake-1"},
	}
	providers := map[string]provider.Provider{
		"ollama":    ollama,
		"anthropic": anthropic,
		"fake":      fake,
	}
	r := New(cfg, providers)

	resp, providerName, err := r.Do(context.Background(), openai.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.ID != "anth-1" {
		t.Errorf("expected anth-1, got %q", resp.ID)
	}
	if providerName != "anthropic" {
		t.Errorf("providerName = %q, want anthropic", providerName)
	}
	if ollama.DoCalls() != 1 {
		t.Errorf("ollama.DoCalls = %d, want 1", ollama.DoCalls())
	}
	if anthropic.DoCalls() != 1 {
		t.Errorf("anthropic.DoCalls = %d, want 1", anthropic.DoCalls())
	}
	if fake.DoCalls() != 0 {
		t.Errorf("fake.DoCalls = %d, want 0", fake.DoCalls())
	}
}

// TestRouter_Scenario_AllFail — все три падают, возвращается последняя ошибка.
func TestRouter_Scenario_AllFail(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "gpt-4o", Routes: []string{"ollama", "anthropic", "fake"}},
		},
	}
	ollama := &fakeProviderControls{name: "ollama", err: provider.ErrProviderUnavailable}
	anthropic := &fakeProviderControls{name: "anthropic", err: provider.ErrRateLimited}
	fake := &fakeProviderControls{name: "fake", err: provider.ErrTimeout}
	providers := map[string]provider.Provider{
		"ollama":    ollama,
		"anthropic": anthropic,
		"fake":      fake,
	}
	r := New(cfg, providers)

	_, _, err := r.Do(context.Background(), openai.ChatRequest{Model: "gpt-4o"})
	if !errors.Is(err, provider.ErrTimeout) {
		t.Fatalf("expected ErrTimeout (last error), got %v", err)
	}
	if ollama.DoCalls() != 1 || anthropic.DoCalls() != 1 || fake.DoCalls() != 1 {
		t.Errorf("expected each provider called once, got ollama=%d anthropic=%d fake=%d",
			ollama.DoCalls(), anthropic.DoCalls(), fake.DoCalls())
	}
}

// TestRouter_Scenario_StreamFallback — Ollama падает, Anthropic отдаёт стрим.
func TestRouter_Scenario_StreamFallback(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "gpt-4o", Routes: []string{"ollama", "anthropic"}},
		},
	}
	ollama := &fakeProviderControls{name: "ollama", err: provider.ErrProviderUnavailable}
	anthropic := &fakeProviderControls{
		name:   "anthropic",
		chunks: []string{`{"content":"A"}`, `{"content":"B"}`, `{"content":"C"}`},
	}
	providers := map[string]provider.Provider{
		"ollama":    ollama,
		"anthropic": anthropic,
	}
	r := New(cfg, providers)

	st, providerName, err := r.Stream(context.Background(), openai.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = st.Close() }()

	if providerName != "anthropic" {
		t.Errorf("providerName = %q, want anthropic", providerName)
	}

	var got []string
	for {
		ev, err := st.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		got = append(got, ev.Data)
	}

	if len(got) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got))
	}
	if !strings.Contains(got[0], "A") || !strings.Contains(got[2], "C") {
		t.Errorf("unexpected chunks: %v", got)
	}
	if ollama.StreamCalls() != 1 {
		t.Errorf("ollama.StreamCalls = %d, want 1", ollama.StreamCalls())
	}
	if anthropic.StreamCalls() != 1 {
		t.Errorf("anthropic.StreamCalls = %d, want 1", anthropic.StreamCalls())
	}
}

// TestRouter_Scenario_StreamAbortMidway — Ollama рвёт стрим на 2-м чанке.
func TestRouter_Scenario_StreamAbortMidway(t *testing.T) {
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "gpt-4o", Routes: []string{"ollama"}},
		},
	}
	ollama := &fakeProviderControls{
		name:         "ollama",
		chunks:       []string{`{"content":"A"}`, `{"content":"B"}`, `{"content":"C"}`},
		streamChunks: 2,
	}
	providers := map[string]provider.Provider{"ollama": ollama}
	r := New(cfg, providers)

	st, providerName, err := r.Stream(context.Background(), openai.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = st.Close() }()

	if providerName != "ollama" {
		t.Errorf("providerName = %q, want ollama", providerName)
	}

	var got int
	for {
		_, err := st.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if !strings.Contains(err.Error(), "stream aborted") {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
		got++
	}
	if got != 2 {
		t.Errorf("got %d chunks before abort, want 2", got)
	}
}
