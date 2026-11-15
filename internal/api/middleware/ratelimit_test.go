package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/limits"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

// withKey кладёт *repo.Key в контекст, эмулируя то, что делает Auth.
func withKey(r *http.Request, key *repo.Key) *http.Request {
	ctx := context.WithValue(r.Context(), keyContextKey, key)
	return r.WithContext(ctx)
}

func TestRateLimit_AllowsUpToLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := limits.NewStore(3, 100, time.Minute) // 3 запроса в минуту
	mw := RateLimit(logger, store)

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	key := &repo.Key{ID: 1, KeyHash: "test-hash", Owner: "test"}

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
		req = withKey(req, key)
		rr := httptest.NewRecorder()
		mw(next).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	if called != 3 {
		t.Errorf("next called %d times, want 3", called)
	}
}

func TestRateLimit_ExceedsLimitReturns429(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := limits.NewStore(1, 100, time.Minute) // 1 запрос в минуту
	mw := RateLimit(logger, store)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	key := &repo.Key{ID: 1, KeyHash: "test-hash", Owner: "test"}

	// Первый запрос — 200.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req = withKey(req, key)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rr.Code)
	}

	// Второй — 429 (бакет пуст).
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req2 = withKey(req2, key)
	rr2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "rate_limit_error") {
		t.Errorf("expected OpenAI-format rate_limit_error, got: %s", rr2.Body.String())
	}
}

func TestRateLimit_NoKeyInContextReturns429(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := limits.NewStore(60, 100000, time.Minute)
	mw := RateLimit(logger, store)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called without key in context")
	})

	// Запрос без ключа в контексте (эмуляция неправильного порядка middleware).
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	// Middleware должен вернуть 429 (или 500), но не паниковать.
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestRateLimit_DifferentKeysHaveSeparateBuckets(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := limits.NewStore(1, 100, time.Minute) // 1 запрос на ключ
	mw := RateLimit(logger, store)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	key1 := &repo.Key{ID: 1, KeyHash: "hash-1", Owner: "owner-1"}
	key2 := &repo.Key{ID: 2, KeyHash: "hash-2", Owner: "owner-2"}

	// key1: первый запрос — 200.
	req1 := withKey(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), key1)
	rr1 := httptest.NewRecorder()
	mw(next).ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("key1 first request: expected 200, got %d", rr1.Code)
	}

	// key1: второй — 429.
	req1b := withKey(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), key1)
	rr1b := httptest.NewRecorder()
	mw(next).ServeHTTP(rr1b, req1b)
	if rr1b.Code != http.StatusTooManyRequests {
		t.Errorf("key1 second request: expected 429, got %d", rr1b.Code)
	}

	// key2: первый запрос — 200 (свой бакет, не затронут key1).
	req2 := withKey(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), key2)
	rr2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("key2 first request: expected 200, got %d", rr2.Code)
	}
}
