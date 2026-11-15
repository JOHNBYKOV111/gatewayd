package usage

import (
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

// Tokens — каноническая структура токенов для учёта и биллинга.
// Отличается от openai.Usage тем, что это наша внутренняя модель,
// не зависящая от формата конкретного провайдера.
type Tokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ExtractTokens извлекает токены из ответа провайдера.
// Если Usage == nil (провайдер не вернул usage), возвращает нули.
// Если провайдер вернул неконсистентные значения (total != prompt + completion),
// возвращает как есть — мы не «исправляем» провайдера, а учитываем то,
// что он реально списал.
func ExtractTokens(resp openai.ChatResponse) Tokens {
	if resp.Usage == nil {
		return Tokens{}
	}
	return Tokens{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
}
