package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johnbykov1989/gatewayd/internal/api/handlers"
	"github.com/johnbykov1989/gatewayd/internal/api/middleware"
	"github.com/johnbykov1989/gatewayd/internal/limits"
	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
	"github.com/johnbykov1989/gatewayd/internal/routing"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
)

func NewRouter(
	logger *slog.Logger,
	router *routing.Router,
	chatHandler *handlers.ChatHandler,
	keyRepo *repo.KeyRepo,
	budgetRepo *repo.BudgetRepo,
	callLogRepo *repo.CallLogRepo,
	m *metrics.Metrics,
	tr *tracing.Tracer,
	masterKey string,
	limitsStore *limits.Store,
) http.Handler {
	r := chi.NewRouter()

	// Глобальные middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.Tracing(tr))
	r.Use(middleware.RequestLogger(logger))
	r.Use(middleware.Recover)
	r.Use(middleware.Timeout(300 * time.Second)) // Увеличено для Ollama на CPU (может думать 1-2 минуты)
	r.Use(middleware.TTFT(m))

	// Публичные эндпоинты (без auth)
	r.Get("/healthz", handleHealthz)
	r.Handle("/metrics", m.Handler())

	// Защищённые эндпоинты
	r.Group(func(r chi.Router) {
		// ВАЖНО: chi выполняет middleware в порядке СВЕРХУ ВНИЗ.
		// Auth должен быть ПЕРВЫМ, чтобы положить ключ в контекст,
		// иначе RateLimit и Budget упадут.
		r.Use(middleware.Auth(logger, keyRepo, masterKey))
		r.Use(middleware.RateLimit(logger, limitsStore))
		r.Use(middleware.Budget(logger, budgetRepo, "daily"))

		// OpenAI-совместимые
		r.Post("/v1/chat/completions", chatHandler.Handle)
		r.Get("/v1/models", handlers.Models())

		// Админка
		admin := handlers.NewAdminKeys(keyRepo, callLogRepo)
		r.Post("/admin/keys", admin.CreateKey)
		r.Get("/admin/keys", admin.ListKeys)
		r.Get("/admin/usage", admin.Usage)
	})

	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
