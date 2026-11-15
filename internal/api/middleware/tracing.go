package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
)

// Tracing возвращает middleware, которое создаёт root-span на каждый HTTP-запрос.
//
// Важно: middleware НЕ оборачивает http.ResponseWriter. Иначе SSE-стримы
// потеряют http.Flusher и стриминг сломается. Middleware только кладёт span
// в контекст запроса — все хендлеры и вложенные вызовы используют его как родителя.
//
// nil Tracer безопасен: middleware пропускает запрос без создания span.
func Tracing(tr *tracing.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tr == nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx, span := tr.Start(r.Context(), "http.request")
			defer span.End()

			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.path", r.URL.Path),
			)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
