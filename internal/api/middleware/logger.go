package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/obs/logging"
)

func RequestLogger(baseLogger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			next.ServeHTTP(w, r)

			logger := logging.WithRequestID(r.Context(), baseLogger)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
