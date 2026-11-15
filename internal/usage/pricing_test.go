package usage

import (
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/config"
)

func TestCalculateCost_USDPerMillion(t *testing.T) {
	// Цена: $2.5 за 1M input, $10 за 1M output.
	price := config.Price{Input: 2.5, Output: 10.0}
	tokens := Tokens{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}

	cost := CalculateCost(tokens, price)
	// 1M input = $2.5, 1M output = $10, итого $12.5.
	if cost != 12.5 {
		t.Errorf("cost = %v, want 12.5", cost)
	}
}

func TestCalculateCost_SmallNumbers(t *testing.T) {
	// Цена: $2.5 за 1M input, $10 за 1M output.
	price := config.Price{Input: 2.5, Output: 10.0}
	tokens := Tokens{PromptTokens: 1000, CompletionTokens: 500}

	// 1000 input = 1000/1M * 2.5 = 0.0025
	// 500 output = 500/1M * 10 = 0.005
	// Итого: 0.0075
	cost := CalculateCost(tokens, price)
	if cost < 0.0074 || cost > 0.0076 {
		t.Errorf("cost = %v, want ~0.0075", cost)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	price := config.Price{Input: 2.5, Output: 10.0}
	cost := CalculateCost(Tokens{}, price)
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
}

func TestCalculateCost_ZeroPrice(t *testing.T) {
	// Ollama-локальная модель: цена 0.
	price := config.Price{Input: 0, Output: 0}
	tokens := Tokens{PromptTokens: 1000, CompletionTokens: 500}

	cost := CalculateCost(tokens, price)
	if cost != 0 {
		t.Errorf("cost = %v, want 0 for free model", cost)
	}
}

func TestCalculateCost_LargeNumbers(t *testing.T) {
	// Цена: $100 за 1M input, $300 за 1M output (Claude Opus).
	price := config.Price{Input: 100.0, Output: 300.0}
	tokens := Tokens{PromptTokens: 10_000_000, CompletionTokens: 2_000_000}

	// 10M input = 10 * 100 = $1000
	// 2M output = 2 * 300 = $600
	// Итого: $1600
	cost := CalculateCost(tokens, price)
	if cost != 1600.0 {
		t.Errorf("cost = %v, want 1600", cost)
	}
}

func TestFormatCost(t *testing.T) {
	cases := []struct {
		cost float64
		want string
	}{
		{0, "0.000000"},
		{0.0075, "0.007500"},
		{12.5, "12.500000"},
		{1600.0, "1600.000000"},
	}
	for _, tc := range cases {
		got := FormatCost(tc.cost)
		if got != tc.want {
			t.Errorf("FormatCost(%v) = %q, want %q", tc.cost, got, tc.want)
		}
	}
}
