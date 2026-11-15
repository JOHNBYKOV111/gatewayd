package middleware

import (
	"log/slog"
	"net/http"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

// Budget — middleware проверки бюджета перед запросом.
// Должен идти ПОСЛЕ Auth: ему нужен *repo.Key в контексте.
//
// Логика:
//   - если у ключа нет бюджета — пропускаем (безлимитный ключ);
//   - если остаток < 10% лимита — ставим X-GW-Budget-Warning;
//   - если остаток <= 0 — возвращаем 402.
//
// Точное списание бюджета происходит ПОСЛЕ ответа провайдера
// (в chat.go, после получения usage), потому что до ответа мы не знаем,
// сколько токенов потрачено.
func Budget(logger *slog.Logger, budgetRepo *repo.BudgetRepo, period string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := KeyFromContext(r.Context())
			if !ok || key == nil {
				logger.Error("budget: key not found in context (middleware order bug)")
				writeBudgetError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			// Мастер-ключ (ID = -1) не имеет бюджета и не должен проверяться.
			if key.ID == -1 {
				next.ServeHTTP(w, r)
				return
			}

			limit, remaining, err := budgetRepo.LimitAndRemaining(r.Context(), key.ID, period)
			if err != nil {
				if err == repo.ErrBudgetNotFound {
					// Бюджета нет — ключ безлимитный, пропускаем.
					next.ServeHTTP(w, r)
					return
				}
				logger.Error("budget: get budget", "err", err, "key_id", key.ID)
				writeBudgetError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			if remaining <= 0 {
				logger.Warn("budget: exceeded", "key_id", key.ID, "owner", key.Owner, "period", period)
				writeBudgetError(w, http.StatusPaymentRequired, "budget exceeded")
				return
			}

			// Мягкое превышение: остаток < 10% от лимита.
			if remaining < 0.1*limit {
				w.Header().Set("X-GW-Budget-Warning", "budget is low")
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeBudgetError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonv2.MarshalWrite(w, openai.NewErrorResponse("insufficient_quota", message))
}
