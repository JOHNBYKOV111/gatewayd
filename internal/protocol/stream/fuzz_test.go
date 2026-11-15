package stream

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// FuzzReader проверяет, что SSE-ридер не паникует и не зависает
// на произвольных (в том числе битых) данных.
func FuzzReader(f *testing.F) {
	// Seed corpus: добавляем базовые случаи, чтобы фаззер быстрее нашел границы.
	f.Add([]byte("data: {\"a\":1}\n\n"))
	f.Add([]byte("data: [DONE]\n\n"))
	f.Add([]byte("data: line1\ndata: line2\n\n"))
	f.Add([]byte(": comment\n\n"))
	f.Add([]byte("data: {\"a\":1}\n")) // без \n\n в конце
	f.Add([]byte("data: "))            // обрубок
	f.Add([]byte(""))                  // пустой ввод

	f.Fuzz(func(t *testing.T, data []byte) {
		// Каждый fuzz-вызов изолирован по контексту.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		src := strings.NewReader(string(data))
		r := NewReaderWithContext(ctx, src)

		// Читаем до EOF или ошибки. Главное — не зависнуть и не запаниковать.
		for {
			_, err := r.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				// Любая другая ошибка (context.Canceled и т.д.) тоже допустима,
				// главное — не паника.
				return
			}
		}
	})
}
