package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

// testBudgetRepo создаёт реальный BudgetRepo в изолированном Postgres-контейнере.
func testBudgetRepo(t *testing.T) (*repo.BudgetRepo, *repo.KeyRepo, *store.Store) {
	t.Helper()
	dsn := testutil.StartPostgres(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := store.Migrate(dsn, "../../store/migrations"); err != nil {
		st.Close()
		t.Fatalf("store.Migrate: %v", err)
	}
	t.Cleanup(st.Close)

	return repo.NewBudgetRepo(st.Pool), repo.NewKeyRepo(st.Pool), st
}

// createBudgetKey создаёт ключ и возвращает его.
func createBudgetKey(t *testing.T, keyRepo *repo.KeyRepo, owner string) *repo.Key {
	t.Helper()
	id, err := keyRepo.Create(context.Background(), repo.Key{
		KeyHash:       "hash-" + owner,
		KeyPrefix:     "gw-",
		KeyLast4:      "test",
		Owner:         owner,
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}
	return &repo.Key{ID: id, KeyHash: "hash-" + owner, Owner: owner}
}

// withKey кладёт *repo.Key в контекст, эмулируя Auth.
func withBudgetKey(r *http.Request, key *repo.Key) *http.Request {
	ctx := context.WithValue(r.Context(), keyContextKey, key)
	return r.WithContext(ctx)
}

func TestBudget_NoBudgetAllowsRequest(t *testing.T) {
	budgetRepo, keyRepo, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	key := createBudgetKey(t, keyRepo, "owner-no-budget-mw")
	// Бюджет не создан — запрос должен пройти.

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := withBudgetKey(
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
		key,
	)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called when no budget exists")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestBudget_ExceededReturns402(t *testing.T) {
	budgetRepo, keyRepo, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	key := createBudgetKey(t, keyRepo, "owner-exceeded-mw")
	ctx := context.Background()

	// Бюджет 1 ₽, списываем 1 ₽ → остаток 0.
	if err := budgetRepo.Set(ctx, key.ID, "daily", 1.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}
	if _, err := budgetRepo.Spend(ctx, key.ID, "daily", 1.0); err != nil {
		t.Fatalf("Spend: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called when budget exceeded")
	})

	req := withBudgetKey(
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
		key,
	)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "insufficient_quota") {
		t.Errorf("expected insufficient_quota error, got: %s", rr.Body.String())
	}
}

func TestBudget_LowBalanceSetsWarning(t *testing.T) {
	budgetRepo, keyRepo, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	key := createBudgetKey(t, keyRepo, "owner-low-mw")
	ctx := context.Background()

	// Бюджет 10 ₽, списываем 9.5 ₽ → остаток 0.5 (5% от лимита < 10%).
	if err := budgetRepo.Set(ctx, key.ID, "daily", 10.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}
	if _, err := budgetRepo.Spend(ctx, key.ID, "daily", 9.5); err != nil {
		t.Fatalf("Spend: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := withBudgetKey(
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
		key,
	)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called (budget not exhausted)")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-GW-Budget-Warning") == "" {
		t.Error("expected X-GW-Budget-Warning header")
	}
}

func TestBudget_HealthyBudgetNoWarning(t *testing.T) {
	budgetRepo, keyRepo, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	key := createBudgetKey(t, keyRepo, "owner-healthy-mw")
	ctx := context.Background()

	// Бюджет 10 ₽, списываем 1 ₽ → остаток 9 (90% от лимита > 10%).
	if err := budgetRepo.Set(ctx, key.ID, "daily", 10.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}
	if _, err := budgetRepo.Spend(ctx, key.ID, "daily", 1.0); err != nil {
		t.Fatalf("Spend: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := withBudgetKey(
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
		key,
	)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Header().Get("X-GW-Budget-Warning") != "" {
		t.Errorf("expected no X-GW-Budget-Warning, got %q", rr.Header().Get("X-GW-Budget-Warning"))
	}
}

func TestBudget_NoKeyInContextReturns500(t *testing.T) {
	budgetRepo, _, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called without key in context")
	})

	// Запрос без ключа в контексте (эмуляция неправильного порядка middleware).
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

// TestBudget_MasterKeyBypassesCheck проверяет, что мастер-ключ (ID = -1)
// пропускает проверку бюджета, даже если для него нет записи в БД.
func TestBudget_MasterKeyBypassesCheck(t *testing.T) {
	budgetRepo, _, _ := testBudgetRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Budget(logger, budgetRepo, "daily")

	masterKey := &repo.Key{
		ID:      -1,
		KeyHash: "master",
		Owner:   "master",
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := withBudgetKey(
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
		masterKey,
	)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called for master key")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
