// Package testutil содержит общие хелперы для тестов.
// Файлы в этом пакете не попадают в продакшн-сборку,
// потому что зависят от testing и testcontainers.
package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgres поднимает контейнер postgres:18, ждёт готовности,
// прогоняет миграции НЕ здесь (это ответственность теста) и возвращает DSN.
// Контейнер автоматически удаляется через t.Cleanup.
//
// Каждый вызов создаёт отдельный контейнер на случайном порту —
// это гарантирует изоляцию тестов друг от друга.
func StartPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pg, err := postgres.Run(ctx, "postgres:18",
		postgres.WithDatabase("gatewayd"),
		postgres.WithUsername("gatewayd"),
		postgres.WithPassword("gatewayd"),
		// Ждём, пока Postgres дважды напишет "ready to accept connections".
		// Первый раз — для initdb, второй — для реального сервера.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := pg.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}
