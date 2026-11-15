package usage

import (
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

func TestExtractTokens_WithUsage(t *testing.T) {
	resp := openai.ChatResponse{
		Usage: &openai.Usage{
			PromptTokens:     28,
			CompletionTokens: 12,
			TotalTokens:      40,
		},
	}

	got := ExtractTokens(resp)
	if got.PromptTokens != 28 {
		t.Errorf("PromptTokens = %d, want 28", got.PromptTokens)
	}
	if got.CompletionTokens != 12 {
		t.Errorf("CompletionTokens = %d, want 12", got.CompletionTokens)
	}
	if got.TotalTokens != 40 {
		t.Errorf("TotalTokens = %d, want 40", got.TotalTokens)
	}
}

func TestExtractTokens_NilUsage(t *testing.T) {
	// Провайдер не вернул usage (например, Ollama без include_usage).
	// Должны получить нули, а не панику.
	resp := openai.ChatResponse{Usage: nil}

	got := ExtractTokens(resp)
	if got.PromptTokens != 0 || got.CompletionTokens != 0 || got.TotalTokens != 0 {
		t.Errorf("expected zeros for nil usage, got %+v", got)
	}
}

func TestExtractTokens_TotalMismatch(t *testing.T) {
	// Некоторые провайдеры возвращают total != prompt + completion (баг провайдера).
	// Мы должны вернуть как есть, не «исправляя» провайдера.
	resp := openai.ChatResponse{
		Usage: &openai.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      99, // заведомо неверно
		},
	}

	got := ExtractTokens(resp)
	if got.TotalTokens != 99 {
		t.Errorf("TotalTokens = %d, want 99 (as-is from provider)", got.TotalTokens)
	}
}
