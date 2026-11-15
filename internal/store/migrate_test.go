package store

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // регистрирует "pgx5" драйвер
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func TestMigrate_Idempotent(t *testing.T) {
	dsn := testutil.StartPostgres(t)

	// Первый Up: применяем все миграции.
	if err := Migrate(dsn, "migrations"); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	// Второй Up: должен вернуть ErrNoChange (идемпотентность), а не ошибку.
	err := Migrate(dsn, "migrations")
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("second Migrate should be no-op, got: %v", err)
	}

	// Проверяем, что таблицы реально созданы (через pgxpool, не через database/sql).
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	var count int
	row := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('keys','budgets','call_logs')`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 tables (keys, budgets, call_logs), got %d", count)
	}
}

func TestMigrate_DownUp(t *testing.T) {
	dsn := testutil.StartPostgres(t)

	// Применяем миграции.
	if err := Migrate(dsn, "migrations"); err != nil {
		t.Fatalf("Migrate up: %v", err)
	}

	// Откатываем все миграции.
	pgx5DSN := toPgx5DSN(dsn)
	m, err := migrate.New("file://migrations", pgx5DSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Down: %v", err)
	}
	_, _ = m.Close()

	// После Down таблиц не должно быть.
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	var count int
	row := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('keys','budgets','call_logs')`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tables after Down, got %d", count)
	}

	// Возвращаем миграции обратно, чтобы не ломать другие тесты.
	if err := Migrate(dsn, "migrations"); err != nil {
		t.Fatalf("Migrate up again: %v", err)
	}
}
