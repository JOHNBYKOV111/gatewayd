package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"

	"github.com/johnbykov1989/gatewayd/internal/obs/logging"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateUUID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), logging.RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
