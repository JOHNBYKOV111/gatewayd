package middleware

import (
	"log/slog"
	"net/http"

	"github.com/johnbykov1989/gatewayd/internal/obs/logging"
)

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger := logging.WithRequestID(r.Context(), slog.Default())
				logger.Error("panic recovered", "error", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"internal error"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
