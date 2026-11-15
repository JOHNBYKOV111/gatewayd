package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store оборачивает пул соединений pgx.
// Только native API pgxpool.Pool — database/sql не используется нигде.
type Store struct {
	Pool *pgxpool.Pool
}

// New создаёт пул соединений к Postgres по dsn ("postgres://...").
// Пул настраивается через pgxpool.Config, не через database/sql.
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse pgxpool config: %w", err)
	}

	// Продакшн-настройки пула. Можно вынести в конфиг позже.
	cfg.MaxConns = 10
	cfg.MinConns = 2
	// MaxConnLifetime и HealthCheckPeriod оставляем дефолтными pgxpool,
	// они уже разумные (1h и 1m).

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pgxpool: %w", err)
	}

	// Проверяем, что база реально доступна (не только парсинг DSN).
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	return &Store{Pool: pool}, nil
}

// Close закрывает пул соединений. Идемпотентен.
func (s *Store) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}
