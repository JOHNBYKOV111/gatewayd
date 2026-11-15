#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="deploy/docker-compose.yml"
PROMETHEUS_URL="http://localhost:9090"
GRAFANA_URL="http://localhost:3000"
GATEWAY_URL="http://localhost:8080"

echo "==> Запуск стека..."
docker compose -f "$COMPOSE_FILE" up -d --wait

echo "==> Проверка статуса контейнера migrate..."
MIGRATE_ID=$(docker compose -f "$COMPOSE_FILE" ps -a -q migrate)
if [ -z "$MIGRATE_ID" ]; then
  echo "❌ Контейнер migrate не найден"
  exit 1
fi
MIGRATE_STATUS=$(docker inspect -f '{{.State.Status}}' "$MIGRATE_ID")
MIGRATE_EXIT=$(docker inspect -f '{{.State.ExitCode}}' "$MIGRATE_ID")
if [ "$MIGRATE_STATUS" != "exited" ] || [ "$MIGRATE_EXIT" -ne 0 ]; then
  echo "❌ migrate: status=$MIGRATE_STATUS exit=$MIGRATE_EXIT"
  docker compose -f "$COMPOSE_FILE" logs migrate
  exit 1
fi

echo "==> Проверка схемы БД после миграций..."
PG_SCHEMA_CHECK=$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U gatewayd -d gatewayd -tAc "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('keys','budgets','call_logs');")
if [ "$PG_SCHEMA_CHECK" != "3" ]; then
  echo "❌ Ожидалось 3 таблицы (keys, budgets, call_logs), найдено: $PG_SCHEMA_CHECK"
  exit 1
fi
echo "✅ Миграции применены, таблицы существуют"

echo "==> Проверка healthcheck gatewayd..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY_URL/healthz" || true)
if [ "$HTTP_CODE" != "200" ]; then
  echo "❌ gatewayd не healthy (HTTP $HTTP_CODE)"
  exit 1
fi
echo "✅ gatewayd healthy"

echo "==> Проверка метрик /metrics..."
METRICS=$(curl -s "$GATEWAY_URL/metrics")
for METRIC in gw_requests_total gw_ttft_seconds gw_cost_rubles_total gw_provider_errors_total gw_cache_hit_ratio; do
  if ! echo "$METRICS" | grep -q "$METRIC"; then
    echo "❌ Метрика $METRIC отсутствует"
    exit 1
  fi
done
echo "✅ Все 5 метрик на месте"

echo "==> Проверка таргета Prometheus..."
PROM_TARGETS=$(curl -s "$PROMETHEUS_URL/api/v1/targets?state=active")
if ! echo "$PROM_TARGETS" | grep -q '"job":"gatewayd"' || ! echo "$PROM_TARGETS" | grep -q '"health":"up"'; then
  echo "❌ Prometheus не видит gatewayd как UP"
  exit 1
fi
echo "✅ Prometheus видит gatewayd"

echo "==> Проверка дашборда Grafana..."
GRAFANA_DASH=$(curl -s -u admin:admin "$GRAFANA_URL/api/dashboards/uid/gatewayd-main")
if ! echo "$GRAFANA_DASH" | grep -q '"uid":"gatewayd-main"'; then
  echo "❌ Дашборд gatewayd-main не найден в Grafana"
  exit 1
fi
echo "✅ Дашборд доступен в Grafana"

echo "🎉 стек поднят, миграции применены, метрики и дашборд на месте."