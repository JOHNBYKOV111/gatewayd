package usage

import (
	"fmt"

	"github.com/johnbykov1989/gatewayd/internal/config"
)

// CalculateCost рассчитывает стоимость вызова в долларах (или рублях,
// в зависимости от того, в каких единицах заданы цены в конфиге).
//
// Формула: (prompt_tokens / 1_000_000) * price.Input
//   - (completion_tokens / 1_000_000) * price.Output.
//
// Цены в конфиге заданы за 1 миллион токенов (как у OpenAI, Anthropic и т.д.).
// Для бесплатных моделей (Ollama) price.Input == 0 и price.Output == 0,
// поэтому стоимость всегда 0.
func CalculateCost(tokens Tokens, price config.Price) float64 {
	const million = 1_000_000.0
	inputCost := (float64(tokens.PromptTokens) / million) * price.Input
	outputCost := (float64(tokens.CompletionTokens) / million) * price.Output
	return inputCost + outputCost
}

// FormatCost форматирует стоимость с 6 знаками после запятой.
// Это нужно, чтобы в журнале и логах не терять копейки (0.0075, а не 0.01).
func FormatCost(cost float64) string {
	return fmt.Sprintf("%.6f", cost)
}
