package middleware

import (
	"context"
	"log/slog"
	"net/http"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/limits"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

// RateLimit — middleware ограничения скорости per key.
// Должен идти ПОСЛЕ Auth: ему нужен *repo.Key в контексте.
//
// Семантика:
//   - RPM: каждый запрос списывает 1 токен из RPM-бакета.
//   - TPM: точное число токенов известно только после ответа провайдера,
//     поэтому здесь мы не списываем TPM. Точное списание TPM происходит
//     в usage/логировании (П24/П28). Здесь проверяется только RPM.
//
// Осознанное ограничение (см. AGENTS.md и README): лимиты in-memory,
// сбрасываются при рестарте, при двух репликах эффективный RPM удваивается.
func RateLimit(logger *slog.Logger, store *limits.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := KeyFromContext(r.Context())
			if !ok || key == nil {
				// Auth должен был положить ключ в контекст. Если его нет —
				// это ошибка порядка middleware, а не клиента.
				logger.Error("ratelimit: key not found in context (middleware order bug)")
				writeRateLimitError(w, "internal server error")
				return
			}

			if !store.AllowRPM(key.KeyHash) {
				logger.Warn("ratelimit: rpm exceeded",
					"key_id", key.ID,
					"owner", key.Owner,
				)
				writeRateLimitError(w, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitError отдаёт 429 в формате OpenAI.
func writeRateLimitError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = jsonv2.MarshalWrite(w, openai.NewErrorResponse("rate_limit_error", message))
}

// KeyFromContext достаёт *repo.Key из контекста.
// Если ключа нет — возвращает (nil, false).
// Экспортируется, чтобы хендлеры тоже могли достать ключ (для usage/бюджетов).
func KeyFromContext(ctx context.Context) (*repo.Key, bool) {
	v := ctx.Value(keyContextKey)
	if v == nil {
		return nil, false
	}
	k, ok := v.(*repo.Key)
	return k, ok
}
