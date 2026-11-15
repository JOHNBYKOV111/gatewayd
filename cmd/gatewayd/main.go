package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/api"
	"github.com/johnbykov1989/gatewayd/internal/api/handlers"
	"github.com/johnbykov1989/gatewayd/internal/cache"
	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/limits"
	"github.com/johnbykov1989/gatewayd/internal/obs/logging"
	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
	"github.com/johnbykov1989/gatewayd/internal/provider"
	"github.com/johnbykov1989/gatewayd/internal/provider/anthropic"
	"github.com/johnbykov1989/gatewayd/internal/provider/ollama"
	"github.com/johnbykov1989/gatewayd/internal/routing"
	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
	"github.com/johnbykov1989/gatewayd/internal/transport"
)

const shutdownTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gatewayd:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "configs/config.example.yaml", "path to config file")
	flag.Parse()

	logger := logging.New(os.Getenv("GW_LOG_LEVEL"))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.New(ctx, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	if err := store.Migrate(cfg.Database.DSN, "internal/store/migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	keyRepo := repo.NewKeyRepo(st.Pool)
	budgetRepo := repo.NewBudgetRepo(st.Pool)
	callLogRepo := repo.NewCallLogRepo(st.Pool)

	masterKey := os.Getenv("GW_MASTER_KEY")
	if masterKey == "" {
		logger.Warn("GW_MASTER_KEY not set; admin endpoints will be inaccessible without virtual keys")
	}

	limitsStore := limits.NewStore(cfg.Limits.DefaultRPM, cfg.Limits.DefaultTPM, time.Minute)

	var exactCache *cache.Cache
	if cfg.Cache.Enabled {
		ttl, err := time.ParseDuration(cfg.Cache.TTL)
		if err != nil {
			return fmt.Errorf("cache ttl: %w", err)
		}
		exactCache = cache.New(ttl, 1000)
		logger.Info("exact cache enabled", "ttl", ttl, "capacity", 1000)
	} else {
		logger.Info("exact cache disabled")
	}

	m := metrics.New()
	tr := tracing.New(nil)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tr.Shutdown(shutdownCtx); err != nil {
			logger.Error("tracing shutdown", "err", err)
		}
	}()

	providers, err := buildProviders(cfg)
	if err != nil {
		return err
	}

	router := routing.New(cfg, providers)

	chatHandler := handlers.NewChatCompletions(logger, router, callLogRepo, cfg, exactCache, m, tr)

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.NewRouter(
			logger,
			router,
			chatHandler,
			keyRepo,
			budgetRepo,
			callLogRepo,
			m,
			tr,
			masterKey,
			limitsStore,
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      300 * time.Second, // Увеличено для LLM (GigaChat может отвечать долго)
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("gatewayd starting", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case sig := <-stop:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	chatHandler.Stop()

	logger.Info("gatewayd stopped")
	return nil
}

func buildProviders(cfg *config.Config) (map[string]provider.Provider, error) {
	baseClient := transport.NewClient(transport.DefaultTimeouts())
	baseClient.Transport = transport.NewRetryTransport(baseClient.Transport, transport.DefaultRetryConfig())

	out := make(map[string]provider.Provider)
	for _, p := range cfg.Providers {
		switch p.Type {
		case "openai":
			out[p.Name] = ollama.New(p.Name, p.BaseURL, os.Getenv(p.APIKeyEnv), baseClient)
		case "anthropic":
			out[p.Name] = anthropic.New(p.Name, p.BaseURL, os.Getenv(p.APIKeyEnv), baseClient)
		default:
			return nil, fmt.Errorf("unknown provider type: %s", p.Type)
		}
	}
	return out, nil
}
