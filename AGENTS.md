# AGENTS.md — правила работы с проектом gatewayd

## О проекте

`gatewayd` — самостоятельный LLM-шлюз на Go. Совместим с OpenAI API, поддерживает маршрутизацию между провайдерами (Ollama, Anthropic, GigaChat), автоматическое переключение при сбоях (fallback), лимиты, учёт расходов, кэширование, метрики Prometheus и трассировку OTel.

**Стек:** Go 1.27.4+, Postgres 18 (pgx v5), chi, Prometheus, Grafana, OTel, Docker Compose.

## TDD (Test-Driven Development)

**Главное правило:** тесты пишутся **до** кода.

1. Сначала пишется тест, который падает (`go test` → `FAIL`).
2. Затем пишется минимальный код, чтобы тест прошел (`go test` → `PASS`).
3. Затем рефакторинг (если нужно).

**Исключения:**
- Инфраструктурный код (Dockerfile, docker-compose, конфиги) — тесты не требуются.
- Сгенерированный код (если есть) — тесты не требуются.
- Для новых фич **всегда** TDD. Для багфиксов — сначала тест, который воспроизводит баг, потом фикс.

## Правила работы с БД (internal/store/)

1. **Только native API pgx v5** (`pgxpool.Pool`, `pgx.Tx`). **Никакого `database/sql`** нигде в проекте.
2. Пул — через `pgxpool.Config` (MaxConns, MinConns, MaxConnLifetime, HealthCheckPeriod). Не через `SetMaxOpenConns`.
3. Миграции — `internal/store/migrations/*.sql` через `golang-migrate` с нативным драйвером `database/pgx/v5`.
4. DSN в двух видах: `postgres://...` для `pgxpool.New`, `pgx5://...` для `migrate`. Конвертация — только в `Migrate`, не в `New`.
5. Тесты репозиториев — **против реального Postgres** (Testcontainers). Схема миграций фиксируется тестом идемпотентности.

## Правила работы с JSON

1. **Внешние данные** (запросы клиентов, ответы провайдеров) — только через `jsonv2.DefaultOptionsV1()` (прощает невалидный UTF-8 и дубликаты ключей).
2. **Внутренние структуры** (конфиги, БД) — строгие `jsonv2` defaults.
3. Иначе шлюз будет «строже, чем сам OpenAI», что сломает совместимость.

## Правила работы с middleware (internal/api/middleware/)

Порядок middleware в `chi` **критичен**:
```go
r.Use(middleware.Auth(...))     // 1-й (кладет ключ в контекст)
r.Use(middleware.RateLimit(...)) // 2-й (проверяет лимиты)
r.Use(middleware.Budget(...))    // 3-й (проверяет бюджет)
Auth всегда первым, иначе RateLimit и Budget упадут с key not found in context.

Правила работы с таймаутами
transport.DefaultTimeouts() — ResponseHeaderTimeout = 300 секунд (для Ollama на CPU).

middleware.Timeout(300s) — общий таймаут запроса (для LLM, которые думают долго).

http.Server.WriteTimeout = 300 секунд (в main.go).

Не уменьшать эти значения без веской причины (иначе Ollama на CPU будет падать по таймауту).

Правила работы с секретами
Никогда не коммитить реальные ключи в репозиторий.

Секреты передаются через переменные окружения (GW_MASTER_KEY, GIGACHAT_CREDENTIALS).

Для локальной разработки — файл deploy/gpt2giga/.env (в .gitignore).

На скриншотах для README — использовать переменные ($GW_KEY), а не реальные ключи.

Правила работы с тестами
Все тесты — с -race (детектор гонок данных).

Все тесты — с -p 1 (последовательно), чтобы Testcontainers не перегружали Docker.

Команда для полного прогона:

bash
go test -race -p 1 ./... -count=1
Fuzz-тесты — отдельно:

bash
go test ./internal/protocol/stream -fuzz=FuzzReader -fuzztime=30s
Линтер — make lint → 0 issues.

Безопасность — govulncheck ./... → No vulnerabilities found.

Правила работы с провайдерами (internal/provider/)
Каждый адаптер реализует интерфейс provider.Provider (Do, Stream, HealthCheck, Usage, Capabilities).

Golden-тесты — обязательны (конкретный запрос → конкретный ответ).

Стриминг — обязателен (SSE, [DONE]).

Ошибки — нормализуются в provider.ErrTimeout, ErrRateLimited, ErrUnauthorized и т.д.

Правила работы с метриками (internal/obs/metrics/)
Только 5 метрик (по ТЗ П36):

gw_requests_total{provider,model,status}

gw_ttft_seconds

gw_cost_rubles_total

gw_provider_errors_total

gw_cache_hit_ratio

Не добавлять новые метрики без согласования (это увеличит кардинальность).

nil-safe — обязательно (для тестов, где метрики не нужны).

Правила работы с трассировкой (internal/obs/tracing/)
Root-span: http.request (создается в middleware.Tracing).

Вложенный span: provider.attempt (создается в chat.go).

Атрибуты: key.id, model, provider.

nil-safe — обязательно (для тестов, где трассировка не нужна).

Что делать при добавлении новой фичи
Написать тест (падающий).

Написать код (чтобы тест прошел).

Проверить go test -race -p 1 ./... -count=1.

Проверить make lint.

Обновить README.md (если фича видима пользователю).

Закоммитить с осмысленным сообщением.

Известные ограничения (v1.0.0)
Лимиты скорости — в памяти (сбрасываются при рестарте, при двух репликах RPM удваивается). Redis — вне рамок.

Auth зависит от Postgres — при падении БД шлюз возвращает 401. Кэш ключей в памяти — запланировано в v1.1.0.

Стоимость стрима — приблизительная до последнего фрагмента.

previous_response_id — не поддерживается между провайдерами.

govulncheck — 3 уязвимости в модулях, но код их не вызывает (безопасно).

Полезные команды
bash
make build      # сборка bin/gatewayd
make test       # go test ./...
make lint       # golangci-lint run
make run        # сборка и запуск
make bench      # нагрузочное тестирование через k6

# Полный прогон тестов с race (без перегрузки Docker)
go test -race -p 1 ./... -count=1

# Fuzz-тест SSE-парсера
go test ./internal/protocol/stream -fuzz=FuzzReader -fuzztime=30s

# Проверка безопасности
govulncheck ./...

# Сборка Docker-стека
docker compose -f deploy/docker-compose.yml up -d

# Сквозной тест стека
./scripts/test_deploy.sh
Стиль кода
Go 1.27.4, gofmt, golangci-lint.

Комментарии — только на русском (по ТЗ).

Имена — на английском (по Go-конвенциям).

Ошибки — оборачивать через %w.

Логи — JSON (slog).

Контакты
При вопросах по архитектуре — смотреть README.md и этот файл. При вопросах по коду — смотреть тесты (они документируют поведение).

text

**Что делать:**
1. Скопируйте этот текст в `AGENTS.md` в корне проекта.
2. Сохраните.
3. Сделайте:
   ```bash
   git add AGENTS.md
   git commit -m "docs: add AGENTS.md with project rules and context"
   git tag v1.1.0
   git tag -l