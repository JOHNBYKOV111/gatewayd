package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // регистрирует "pgx5" драйвер
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Migrate применяет миграции из папки migrationsPath к базе dsn.
// dsn должен быть в формате "postgres://...", внутри конвертируется в "pgx5://".
// Повторный вызов безопасен: если миграции уже применены, возвращает migrate.ErrNoChange.
func Migrate(dsn, migrationsPath string) error {
	pgx5DSN := toPgx5DSN(dsn)

	m, err := migrate.New("file://"+migrationsPath, pgx5DSN)
	if err != nil {
		return fmt.Errorf("store: init migrate: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return nil
}

// toPgx5DSN конвертирует "postgres://" или "postgresql://" в "pgx5://".
// Нужно потому, что pgxpool ожидает postgres://, а migrate с драйвером pgx5 — pgx5://.
func toPgx5DSN(dsn string) string {
	dsn = strings.TrimPrefix(dsn, "postgresql://")
	dsn = strings.TrimPrefix(dsn, "postgres://")
	return "pgx5://" + dsn
}
