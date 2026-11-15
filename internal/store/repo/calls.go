package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CallLog — одна запись журнала вызовов.
// Промпт НЕ хранится в plaintext: только хеш (PromptHash).
type CallLog struct {
	Timestamp        time.Time
	KeyID            *int64
	Model            string
	Provider         string
	PromptHash       string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostRubles       float64
	LatencyMS        int
	TTFTMS           int
	Error            string
}

// UsageSummary — агрегированная статистика за период.
type UsageSummary struct {
	TotalRequests int
	TotalTokens   int
	TotalCost     float64
	ByModel       map[string]ModelUsage
	ByKey         map[int64]KeyUsage
}

// ModelUsage — агрегат по модели.
type ModelUsage struct {
	Requests    int
	TotalTokens int
	CostRubles  float64
}

// KeyUsage — агрегат по ключу.
type KeyUsage struct {
	Requests    int
	TotalTokens int
	CostRubles  float64
}

// CallLogRepo — репозиторий журнала вызовов.
// Работает на нативном pgxpool, без database/sql.
type CallLogRepo struct {
	pool *pgxpool.Pool
}

func NewCallLogRepo(pool *pgxpool.Pool) *CallLogRepo {
	return &CallLogRepo{pool: pool}
}

// Record вставляет одну запись в call_logs.
// key_id может быть nil (если ключ удалён или запрос без ключа).
func (r *CallLogRepo) Record(ctx context.Context, log CallLog) error {
	var keyID any
	if log.KeyID != nil {
		keyID = *log.KeyID
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO call_logs (
			timestamp, key_id, model, provider, prompt_hash,
			prompt_tokens, completion_tokens, total_tokens,
			cost_rubles, latency_ms, ttft_ms, error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`,
		log.Timestamp, keyID, log.Model, log.Provider, log.PromptHash,
		log.PromptTokens, log.CompletionTokens, log.TotalTokens,
		log.CostRubles, log.LatencyMS, log.TTFTMS, log.Error,
	)
	if err != nil {
		return fmt.Errorf("repo: record call log: %w", err)
	}
	return nil
}

// UsageSummary возвращает агрегацию по моделям и ключам за период с since.
// Два SELECT: один GROUP BY model, второй GROUP BY key_id.
func (r *CallLogRepo) UsageSummary(ctx context.Context, since time.Time) (*UsageSummary, error) {
	summary := &UsageSummary{
		ByModel: make(map[string]ModelUsage),
		ByKey:   make(map[int64]KeyUsage),
	}

	// Общие итоги + агрегация по моделям.
	modelRows, err := r.pool.Query(ctx, `
		SELECT model, COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost_rubles), 0)
		FROM call_logs
		WHERE timestamp >= $1
		GROUP BY model
	`, since)
	if err != nil {
		return nil, fmt.Errorf("repo: usage by model: %w", err)
	}
	defer modelRows.Close()

	for modelRows.Next() {
		var (
			model    string
			requests int
			tokens   int
			cost     float64
		)
		if err := modelRows.Scan(&model, &requests, &tokens, &cost); err != nil {
			return nil, fmt.Errorf("repo: scan usage by model: %w", err)
		}
		summary.ByModel[model] = ModelUsage{Requests: requests, TotalTokens: tokens, CostRubles: cost}
		summary.TotalRequests += requests
		summary.TotalTokens += tokens
		summary.TotalCost += cost
	}
	if err := modelRows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate usage by model: %w", err)
	}

	// Агрегация по ключам (только записи с key_id IS NOT NULL).
	keyRows, err := r.pool.Query(ctx, `
		SELECT key_id, COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost_rubles), 0)
		FROM call_logs
		WHERE timestamp >= $1 AND key_id IS NOT NULL
		GROUP BY key_id
	`, since)
	if err != nil {
		return nil, fmt.Errorf("repo: usage by key: %w", err)
	}
	defer keyRows.Close()

	for keyRows.Next() {
		var (
			keyID    int64
			requests int
			tokens   int
			cost     float64
		)
		if err := keyRows.Scan(&keyID, &requests, &tokens, &cost); err != nil {
			return nil, fmt.Errorf("repo: scan usage by key: %w", err)
		}
		summary.ByKey[keyID] = KeyUsage{Requests: requests, TotalTokens: tokens, CostRubles: cost}
	}
	if err := keyRows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate usage by key: %w", err)
	}

	return summary, nil
}
