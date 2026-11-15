package usage

import (
	"strings"
	"testing"
)

func TestEstimateTokens_Empty(t *testing.T) {
	got := EstimateTokens("")
	if got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokens_ShortText(t *testing.T) {
	// "Hello world" — примерно 2-3 токена.
	// Мы не требуем точности, но проверяем разумный диапазон.
	got := EstimateTokens("Hello world")
	if got < 1 || got > 10 {
		t.Errorf("EstimateTokens(\"Hello world\") = %d, want 1..10", got)
	}
}

func TestEstimateTokens_RussianText(t *testing.T) {
	// Русский текст: "Привет, мир!" — примерно 3-5 токенов.
	got := EstimateTokens("Привет, мир!")
	if got < 1 || got > 15 {
		t.Errorf("EstimateTokens(\"Привет, мир!\") = %d, want 1..15", got)
	}
}

func TestEstimateTokens_LongText(t *testing.T) {
	// 1000 символов 'a' — примерно 250 токенов (грубо 4 символа на токен).
	text := strings.Repeat("a", 1000)
	got := EstimateTokens(text)
	if got < 100 || got > 500 {
		t.Errorf("EstimateTokens(1000 chars) = %d, want 100..500", got)
	}
}

func TestEstimateTokens_Monotonic(t *testing.T) {
	// Чем длиннее текст, тем больше оценка. Это гарантия, что оценщик
	// не выдаёт случайные числа.
	short := EstimateTokens("a")
	long := EstimateTokens(strings.Repeat("a", 100))
	if long <= short {
		t.Errorf("long text (%d) should estimate more tokens than short (%d)", long, short)
	}
}

func TestEstimatePromptTokens(t *testing.T) {
	// Оценка промпта по списку сообщений.
	msgs := []string{"Hello", "How are you?", "Fine, thanks"}
	got := EstimatePromptTokens(msgs)
	if got < 1 {
		t.Errorf("EstimatePromptTokens = %d, want > 0", got)
	}

	// Пустой список — 0.
	if got := EstimatePromptTokens(nil); got != 0 {
		t.Errorf("EstimatePromptTokens(nil) = %d, want 0", got)
	}
}

func TestEstimateTokens_SingleChar(t *testing.T) {
	// Односимвольный текст: latin/4 и cyrillic/2 дают 0, но реально это 1 токен.
	if got := EstimateTokens("a"); got != 1 {
		t.Errorf("EstimateTokens(\"a\") = %d, want 1", got)
	}
	if got := EstimateTokens("я"); got != 1 {
		t.Errorf("EstimateTokens(\"я\") = %d, want 1", got)
	}
}
