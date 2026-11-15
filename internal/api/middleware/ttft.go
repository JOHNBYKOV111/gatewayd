package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
)

type contextKey string

const (
	// StreamContextKey устанавливается хендлером, когда он определил, что
	// запрос является потоковым (SSE). Middleware TTFT читает этот флаг,
	// чтобы понять, нужно ли замерять время до первого байта.
	StreamContextKey contextKey = "gw_stream_request"
)

// WithStreamFlag добавляет в контекст признак того, что ответ будет потоковым.
// Хендлер должен вызвать эту функцию, как только распарсил запрос и понял,
// что это стрим (например, поле "stream": true).
func WithStreamFlag(ctx context.Context) context.Context {
	return context.WithValue(ctx, StreamContextKey, true)
}

// TTFT возвращает middleware, которое замеряет время до первого токена (TTFT)
// для потоковых запросов. Оно оборачивает ResponseWriter, перехватывает первый
// вызов Write и записывает длительность с момента вызова middleware.
//
// Для не-потоковых запросов (без StreamContextKey в контексте) middleware
// пропускает запрос дальше без замеров, чтобы обычные JSON-ответы не
// засоряли гистограмму TTFT.
func TTFT(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Ранний выход, если это не стрим. Это избавляет от аллокации
			// обёртки для большинства запросов.
			if r.Context().Value(StreamContextKey) != true {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			tw := &ttftResponseWriter{
				ResponseWriter: w,
				start:          start,
				metrics:        m,
			}

			// Если нижележащий writer поддерживает Flush (обязательно для SSE),
			// убеждаемся, что обёртка тоже его предоставляет. Иначе хендлер
			// не сможет отдавать стрим.
			if f, ok := w.(http.Flusher); ok {
				tw.flusher = f
			}

			next.ServeHTTP(tw, r)
		})
	}
}

type ttftResponseWriter struct {
	http.ResponseWriter
	start       time.Time
	metrics     *metrics.Metrics
	flusher     http.Flusher
	wroteHeader bool
	wroteTTFT   bool
}

// WriteHeader перехватывает запись статуса. Здесь TTFT не замеряется,
// потому что заголовки могут быть отправлены задолго до первого токена.
// TTFT фиксируется на первом байте тела ответа.
func (w *ttftResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write перехватывает первую запись тела. Это момент, когда первый фрагмент
// SSE (или первый байт JSON) уходит клиенту — именно это и есть TTFT.
// Фиксируем его ровно один раз.
func (w *ttftResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteTTFT {
		w.wroteTTFT = true
		// w.metrics безопасен для nil, поэтому проверка не нужна.
		w.metrics.ObserveTTFT(time.Since(w.start).Seconds())
	}
	return w.ResponseWriter.Write(b)
}

// Flush делегирует вызов нижележащему writer'у, если он это поддерживает.
// Без этого SSE-стримы буферизовались бы бесконечно, потому что обёртка
// скрывает интерфейс Flusher.
func (w *ttftResponseWriter) Flush() {
	if w.flusher != nil {
		w.flusher.Flush()
	}
}
