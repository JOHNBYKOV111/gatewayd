package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/api/handlers"
	"github.com/johnbykov1989/gatewayd/internal/auth"
	"github.com/johnbykov1989/gatewayd/internal/limits"
	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
	"github.com/johnbykov1989/gatewayd/internal/routing"
	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func TestRouter_Healthz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(
		logger,
		(*routing.Router)(nil),
		(*handlers.ChatHandler)(nil),
		(*repo.KeyRepo)(nil),
		(*repo.BudgetRepo)(nil),
		(*repo.CallLogRepo)(nil),
		(*metrics.Metrics)(nil),
		(*tracing.Tracer)(nil),
		"master-secret",
		(*limits.Store)(nil),
	)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != `{"status":"ok"}` {
		t.Errorf("unexpected body: %s", string(body))
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id header")
	}
}

func TestRouter_Metrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.New()

	m.IncRequests("ollama", "llama3.2:3b", "200")
	m.ObserveTTFT(0.12)
	m.AddCostRubles(0.05)
	m.IncProviderError("ollama", "timeout")
	m.SetCacheHitRatio(0.75)

	router := NewRouter(
		logger,
		(*routing.Router)(nil),
		(*handlers.ChatHandler)(nil),
		(*repo.KeyRepo)(nil),
		(*repo.BudgetRepo)(nil),
		(*repo.CallLogRepo)(nil),
		m,
		(*tracing.Tracer)(nil),
		"master-secret",
		(*limits.Store)(nil),
	)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	body := rr.Body.String()

	for _, metric := range []string{
		"gw_requests_total",
		"gw_ttft_seconds",
		"gw_cost_rubles_total",
		"gw_provider_errors_total",
		"gw_cache_hit_ratio",
	} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing %s in /metrics:\n%s", metric, body)
		}
	}

	if !strings.Contains(body, `gw_requests_total{model="llama3.2:3b",provider="ollama",status="200"} 1`) {
		t.Errorf("gw_requests_total value mismatch:\n%s", body)
	}
	if !strings.Contains(body, `gw_provider_errors_total{class="timeout",provider="ollama"} 1`) {
		t.Errorf("gw_provider_errors_total value mismatch:\n%s", body)
	}
	if !strings.Contains(body, "gw_cost_rubles_total 0.05") {
		t.Errorf("gw_cost_rubles_total value mismatch:\n%s", body)
	}
	if !strings.Contains(body, "gw_cache_hit_ratio 0.75") {
		t.Errorf("gw_cache_hit_ratio value mismatch:\n%s", body)
	}
	if !strings.Contains(body, "gw_ttft_seconds_count 1") {
		t.Errorf("gw_ttft_seconds_count mismatch:\n%s", body)
	}

	if strings.Contains(body, "# metrics placeholder") {
		t.Errorf("/metrics still returns placeholder text:\n%s", body)
	}
}

func TestRouter_MiddlewareChain(t *testing.T) {
	dsn := testutil.StartPostgres(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	if err := store.Migrate(dsn, "../store/migrations"); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}

	keyRepo := repo.NewKeyRepo(st.Pool)
	budgetRepo := repo.NewBudgetRepo(st.Pool)
	callLogRepo := repo.NewCallLogRepo(st.Pool)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rawKey, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := auth.HashKey(rawKey)
	keyID, err := keyRepo.Create(ctx, repo.Key{
		KeyHash:       hash,
		KeyPrefix:     "gw-",
		KeyLast4:      "test",
		Owner:         "owner-middleware",
		AllowedModels: []string{},
		RPMLimit:      1,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}

	router := NewRouter(
		logger,
		(*routing.Router)(nil),
		(*handlers.ChatHandler)(nil),
		keyRepo,
		budgetRepo,
		callLogRepo,
		(*metrics.Metrics)(nil),
		(*tracing.Tracer)(nil),
		"master-secret",
		limits.NewStore(60, 100000, time.Minute),
	)

	// 1. Без ключа → 401 (Auth).
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no key: expected 401, got %d", rr.Code)
	}

	// 2. С ключом, бюджет 0 → 402 (Budget).
	if err := budgetRepo.Set(ctx, keyID, "daily", 0.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("zero budget: expected 402, got %d", rr.Code)
	}
}
