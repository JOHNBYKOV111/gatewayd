package transport

import (
	"net/http"
	"strings"
)

// sensitiveHeaders — заголовки, значения которых нельзя показывать в логах.
// Сравнение регистронезависимое (http.Header канонизирует ключи,
// но мы на всякий случай нормализуем сами).
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"x-api-key":           {},
	"api-key":             {},
	"cookie":              {},
	"set-cookie":          {},
	"x-goog-api-key":      {},
}

// RedactHeaders возвращает копию заголовков, в которой значения
// чувствительных заголовков заменены на "[REDACTED]".
// Оригинальный http.Header не мутируется — это важно, потому что
// он может использоваться дальше в запросе.
func RedactHeaders(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	out := make(http.Header, len(h))
	for k, vv := range h {
		if isSensitive(k) {
			out[k] = []string{"[REDACTED]"}
			continue
		}
		// Копируем срез, чтобы не делить память с оригиналом.
		cp := make([]string, len(vv))
		copy(cp, vv)
		out[k] = cp
	}
	return out
}

func isSensitive(key string) bool {
	_, ok := sensitiveHeaders[strings.ToLower(key)]
	return ok
}
