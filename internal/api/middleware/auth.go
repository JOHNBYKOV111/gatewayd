package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/auth"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

type KeyRepo interface {
	FindByHash(ctx context.Context, hash string) (*repo.Key, error)
}

func Auth(logger *slog.Logger, keyRepo KeyRepo, masterKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeAuthError(w)
				return
			}

			key := strings.TrimPrefix(authHeader, "Bearer ")
			if key == "" {
				writeAuthError(w)
				return
			}

			// Master key пропускает (для /admin/*).
			if masterKey != "" && key == masterKey {
				// ВАЖНО: кладем фиктивный ключ в контекст, чтобы RateLimit и Budget
				// не падали с "key not found in context". ID = -1 означает мастер-ключ.
				fakeKey := &repo.Key{
					ID:        -1,
					KeyHash:   "master",
					KeyPrefix: "gw-master",
					KeyLast4:  "ster",
					Owner:     "master",
					RPMLimit:  0, // 0 = без лимита
					TPMLimit:  0,
				}
				ctx := context.WithValue(r.Context(), keyContextKey, fakeKey)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Проверяем формат ключа (gw-...).
			if !auth.IsValidKeyFormat(key) {
				writeAuthError(w)
				return
			}

			hash := auth.HashKey(key)
			k, err := keyRepo.FindByHash(r.Context(), hash)
			if err != nil || k == nil {
				writeAuthError(w)
				return
			}

			// Кладём ключ в контекст, чтобы хендлер мог использовать лимиты/бюджеты.
			ctx := context.WithValue(r.Context(), keyContextKey, k)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type ctxKey string

const keyContextKey ctxKey = "gw_key"

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = jsonv2.MarshalWrite(w, openai.NewErrorResponse("invalid_request_error", "missing or invalid API key"))
}
