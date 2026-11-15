package middleware

import (
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/auth"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

type fakeKeyRepo struct {
	key *repo.Key
}

func (f *fakeKeyRepo) FindByHash(_ context.Context, hash string) (*repo.Key, error) {
	if f.key != nil && f.key.KeyHash == hash {
		return f.key, nil
	}
	return nil, repo.ErrKeyNotFound
}

func TestAuth_NoKeyReturns401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called without key")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_request_error") {
		t.Errorf("expected OpenAI-format error, got: %s", rr.Body.String())
	}
}

func TestAuth_InvalidKeyReturns401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called with invalid key")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer gw-invalid-key")
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_ValidKeyAllowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rawKey, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := auth.HashKey(rawKey)

	keyRepo := &fakeKeyRepo{
		key: &repo.Key{
			ID:      1,
			KeyHash: hash,
			Owner:   "test",
		},
	}
	mw := Auth(logger, keyRepo, "master-secret")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called with valid key")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// TestAuth_MasterKeyAllowed проверяет, что мастер-ключ пропускает запрос
// и кладет фиктивный ключ в контекст (чтобы RateLimit и Budget не падали).
func TestAuth_MasterKeyAllowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Проверяем, что ключ есть в контексте (иначе RateLimit упадет).
		key, ok := KeyFromContext(r.Context())
		if !ok || key == nil {
			t.Fatal("ожидался ключ в контексте для мастер-ключа")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/keys", nil)
	req.Header.Set("Authorization", "Bearer master-secret")
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called for master key")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuth_ErrorFormat(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	var resp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON error: %v", err)
	}
	if resp.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q", resp.Error.Type)
	}
}

// НОВЫЕ ТЕСТЫ (закрывают auth.go на 100%):

func TestAuth_NonBearerSchemeReturns401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called for non-Bearer scheme")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_EmptyBearerReturns401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called for empty Bearer")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_InvalidFormatReturns401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Auth(logger, &fakeKeyRepo{}, "master-secret")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called for invalid key format")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer not-a-gw-key")
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}
