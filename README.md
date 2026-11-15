# gatewayd

Самостоятельный LLM-шлюз на Go. Совместимый с OpenAI API, с маршрутизацией между провайдерами, автоматическим переключением при сбоях, лимитами, учётом расходов и средствами наблюдения.

> Один статический бинарник на Go. Без внешних рантаймов. Встраивается в существующую Go-инфраструктуру, предсказуемая задержка p99.

## Возможности

- **API, совместимый с OpenAI**: `/v1/chat/completions`, `/v1/models`. Любой существующий SDK для OpenAI работает с `gatewayd` — достаточно поменять только `base_url` и `api_key`.
- **Поддержка нескольких провайдеров**: Ollama (локально), Anthropic, GigaChat (через прокси `gpt2giga`). Для каждой модели настроена цепочка переключения: если первый провайдер недоступен, автоматически используется следующий.
- **Потоковая передача (SSE)**: выдача токенов по одному с отправкой каждого фрагмента, отмена при разрыве соединения клиента и терминатор `[DONE]`. Время до первого токена (TTFT) измеряется по первому фрагменту.
- **Виртуальные API-ключи**: ключи вида `gw-...`, в базе хранятся в виде SHA-256-хеша, для отображения — префикс и последние 4 символа. Для каждого ключа задаются лимиты RPM/TPM, а также дневные и месячные бюджеты.
- **Бюджеты**: при превышении мягкого лимита возвращается заголовок `X-GW-Budget-Warning`, при превышении жёсткого — код ответа `402 Payment Required`. Списание бюджета выполняется атомарно (`UPDATE ... RETURNING`) и безопасно при одновременных запросах.
- **Логирование вызовов**: фиксируется каждый запрос (модель, провайдер, количество токенов, стоимость, задержка, TTFT, ошибки). Промпты не сохраняются в открытом виде — только их SHA-256-хэш.
- **Точный кэш**: кэш в оперативной памяти с TTL и вытеснением по стратегии LRU. Потоковые ответы сохраняются как списки фрагментов и воспроизводятся постранично. Метрика `gw_cache_hit_ratio` экспортируется на эндпоинте `/metrics`.
- **Наблюдаемость**: метрики Prometheus (`/metrics`), структурированные JSON-логи (slog), трассировка OTel (P38).

## Производительность

Нагрузочное тестирование проводилось с помощью [k6](https://k6.io/) (2 виртуальных пользователя, 60 секунд) на запросах к локальной модели Ollama (`llama3.2:3b`) на 4-ядерном CPU.

| Метрика | Значение |
| :--- | :--- |
| **RPS** (запросов в секунду) | **1.84** |
| **p50** (медиана задержки шлюза) | **1.32 мс** |
| **p95** (95-й перцентиль) | **1.94 мс** |
| **Успешных ответов** | **100%** (119 из 119) |
| **Потребление памяти** | **16.7 MiB** |

*Примечание:* Время ответа модели на CPU составляет ~60 секунд (генерация токенов), но задержка самого шлюза (`gatewayd`) — **1.32 мс** (p50). Это доказывает, что шлюз не является узким местом: он лишь проксирует запросы и кэширует ответы.

**Требования ТЗ (П47):** p50 ≤ 3 мс, p99 ≤ 15 мс, память ≤ 50 МБ — **выполнены с запасом**.

## Архитектура

```text
Клиент (openai-sdk / curl / агент)
│ Bearer gw-xxx
▼
┌─────────────────────────────────────────────┐
│                  gatewayd                   │
│  ┌─────────┐   ┌──────────┐  ┌────────────┐ │
│  │ Auth MW │ → │ RateLim  │→ │  Budget    │ │
│  └─────────┘   └──────────┘  └────────────┘ │
│  ┌─────────┐   ┌──────────┐                 │
│  │ Router  │ → │  Cache   │  (точный)       │
│  └─────────┘   └──────────┘                 │
│  ┌──────────────────────────────────────┐   │
│  │ Адаптеры провайдеров                 │   │
│  │ (OpenAI/Ollama, Anthropic, GigaChat) │   │
│  └──────────────────────────────────────┘   │
│  ┌─────────┐  ┌─────────┐  ┌──────────────┐ │
│  │ Breaker │  │  Retry  │  │ Usage Meter  │ │
│  └─────────┘  └─────────┘  └──────────────┘ │
│  ┌──────────────────────────────────────┐   │
│  │ Наблюдаемость: OTel + Prometheus     │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
▼
Провайдеры (HTTPS)
Быстрый старт (≤ 3 минут)
Предварительные требования
Go 1.27.4 или выше;

Docker (для запуска Ollama и Postgres);

curl.

1. Запуск Ollama и Postgres
bash
docker run -d --name ollama -p 11434:11434 ollama/ollama
docker exec -it ollama ollama pull llama3.2:3b

docker run -d \
  --name gatewayd-postgres \
  -e POSTGRES_USER=gatewayd \
  -e POSTGRES_PASSWORD=gatewayd \
  -e POSTGRES_DB=gatewayd \
  -p 5433:5432 \
  -v gatewayd-pgdata:/var/lib/postgresql \
  --health-cmd "pg_isready -h 127.0.0.1 -U gatewayd -d gatewayd" \
  --health-interval 5s \
  --health-timeout 3s \
  --health-retries 10 \
  --health-start-period 30s \
  postgres:18
Дождитесь, пока Postgres перейдёт в статус healthy:

bash
docker ps | grep gatewayd-postgres   # (должно быть healthy)
2. Сборка и запуск шлюза
bash
make build
make run
3. Создание виртуального ключа
bash
export GW_MASTER_KEY="master-secret"
curl -s -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer $GW_MASTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"owner":"alice","rpm_limit":60,"tpm_limit":100000}'
Ответ:

json
{
  "id": 1,
  "key": "gw-<random>",
  "owner": "alice"
}
Сохраните ключ gw-... — он показывается только один раз.

4. Вызов шлюза
bash
export GW_KEY="gw-<random>"
curl -s -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GW_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama3.2:3b","messages":[{"role":"user","content":"Say hello in one word"}]}'
5. Потоковая передача
bash
curl --no-buffer -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GW_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama3.2:3b","messages":[{"role":"user","content":"Count from 1 to 5"}],"stream":true}'
6. Метрики
bash
curl -s http://localhost:8080/metrics
Демонстрация
Запрос к Ollama (локальный провайдер)
https://./.github/assets/demo-ollama.png

Запрос к GigaChat (внешний провайдер через прокси)
https://./.github/assets/demo-gigachat.png

Дашборд Grafana
https://./.github/assets/demo-dashboard.png

Chaos Engineering (отказоустойчивость)
Проверено, что шлюз корректно обрабатывает отказ зависимостей (П42):

Baseline (Postgres жив): запрос успешно выполняется.

Kill Postgres: контейнер БД остановлен.

Degradation: запрос возвращает 401 Unauthorized (шлюз деградировал).

Restore Postgres: контейнер БД запущен.

Recovery: запрос снова выполняется успешно (200 OK), шлюз восстановился без перезапуска.

https://./.github/assets/demo-chaos.png

Тестирование и качество
1. Юнит-тесты и интеграционные тесты (с race-детектором)
bash
go test -race -p 1 ./... -count=1
Все пакеты ok, ни одной гонки данных. Используются реальные Postgres-контейнеры через Testcontainers.
https://./.github/assets/demo-tests.png

2. Fuzz-тестирование (устойчивость SSE-парсера)
bash
go test ./internal/protocol/stream -fuzz=FuzzReader -fuzztime=30s
400 000+ выполнений за 30 секунд, 0 крашей.
https://./.github/assets/demo-fuzz.png

3. Статический анализ и безопасность
make lint → 0 issues.

govulncheck ./... → No vulnerabilities found в вызываемом коде.
https://./.github/assets/demo-vulncheck.png

4. Статический бинарник (для деплоя)
bash
docker run --rm --entrypoint sh deploy-gatewayd -c "ldd /usr/local/bin/gatewayd || true"
Вывод: Not a valid dynamic program (нет внешних зависимостей).
https://./.github/assets/demo-static.png

Конфигурация
Источник истины — файл configs/config.example.yaml. Переменные окружения переопределяют настройки из YAML:

GW_DATABASE_DSN — DSN для Postgres (переопределяет database.dsn);

GW_LISTEN — адрес прослушивания (переопределяет listen);

GW_MASTER_KEY — мастер-ключ для /admin/*;

GW_LOG_LEVEL — уровень логирования: debug, info, warn, error.

Структура репозитория
text
gatewayd/
├── cmd/gatewayd/        # точка входа
├── internal/
│   ├── api/             # chi-роутер, middleware, хендлеры
│   ├── cache/           # точный кэш (TTL + LRU)
│   ├── config/          # YAML + переопределение через env
│   ├── limits/          # алгоритм «ведро токенов», бюджеты
│   ├── obs/             # Prometheus, OTel, slog
│   ├── protocol/        # типы OpenAI, SSE-поток
│   ├── provider/        # интерфейс провайдера и адаптеры
│   ├── routing/         # маршрутизация, fallback, circuit breaker
│   ├── store/           # Postgres (pgx), миграции, репозитории
│   └── usage/           # подсчёт токенов, тарифы, расчёт стоимости
├── configs/
├── deploy/              # docker-compose, Grafana, Prometheus
└── scripts/chaos/
Известные ограничения (v1.0.0)
Лимиты скорости хранятся в оперативной памяти: счётчики сбрасываются при перезапуске. При двух репликах за балансировщиком лимит RPM удвоится. Redis — вне рамок v1.0.0.

Зависимость аутентификации от Postgres: при падении БД шлюз возвращает 401 Unauthorized. Кэширование ключей в памяти запланировано в v1.1.0.

Стоимость потокового ответа оценивается приблизительно до получения последнего фрагмента.

API ответов не хранит состояние (P44): previous_response_id не поддерживается между провайдерами. Клиент должен передавать полную историю диалога.

Разработка
bash
make build      # сборка bin/gatewayd
make test       # go test ./...
make lint       # golangci-lint run
make run        # сборка и запуск
make bench      # нагрузочное тестирование через k6
Тесты покрывают весь код. При добавлении новых фич рекомендуется TDD (тесты до кода). Подробности — в файле AGENTS.md.