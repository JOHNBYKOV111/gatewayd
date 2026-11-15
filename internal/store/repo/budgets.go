package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrBudgetExceeded = errors.New("repo: budget exceeded")
	ErrBudgetNotFound = errors.New("repo: budget not found")
)

// BudgetRepo — репозиторий бюджетов (daily/monthly) для ключей.
// Работает на нативном pgxpool, без database/sql.
type BudgetRepo struct {
	pool *pgxpool.Pool
}

func NewBudgetRepo(pool *pgxpool.Pool) *BudgetRepo {
	return &BudgetRepo{pool: pool}
}

// Set создаёт или обновляет бюджет для ключа и периода.
// period: "daily" | "monthly".
// spent_rubles сбрасывается в 0 при создании.
func (r *BudgetRepo) Set(ctx context.Context, keyID int64, period string, limit float64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO budgets (key_id, period, limit_rubles, spent_rubles, reset_at)
		VALUES ($1, $2, $3, 0, NOW() + interval '1 day')
		ON CONFLICT (key_id, period) DO UPDATE
		SET limit_rubles = EXCLUDED.limit_rubles
	`, keyID, period, limit)
	if err != nil {
		return fmt.Errorf("repo: set budget: %w", err)
	}
	return nil
}

// Spend атомарно списывает amount с бюджета ключа.
// Возвращает остаток (remaining) после списания.
func (r *BudgetRepo) Spend(ctx context.Context, keyID int64, period string, amount float64) (float64, error) {
	var remaining float64
	err := r.pool.QueryRow(ctx, `
		UPDATE budgets
		SET spent_rubles = spent_rubles + $1
		WHERE key_id = $2 AND period = $3 AND spent_rubles + $1 <= limit_rubles
		RETURNING limit_rubles - spent_rubles
	`, amount, keyID, period).Scan(&remaining)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			exists, checkErr := r.exists(ctx, keyID, period)
			if checkErr != nil {
				return 0, checkErr
			}
			if !exists {
				return 0, ErrBudgetNotFound
			}
			return 0, ErrBudgetExceeded
		}
		return 0, fmt.Errorf("repo: spend budget: %w", err)
	}
	return remaining, nil
}

// Remaining возвращает остаток бюджета для ключа и периода.
func (r *BudgetRepo) Remaining(ctx context.Context, keyID int64, period string) (float64, error) {
	var remaining float64
	err := r.pool.QueryRow(ctx, `
		SELECT limit_rubles - spent_rubles FROM budgets
		WHERE key_id = $1 AND period = $2
	`, keyID, period).Scan(&remaining)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrBudgetNotFound
		}
		return 0, fmt.Errorf("repo: get remaining: %w", err)
	}
	return remaining, nil
}

// LimitAndRemaining возвращает лимит и остаток бюджета одним запросом.
// Используется middleware Budget: один SELECT вместо двух.
func (r *BudgetRepo) LimitAndRemaining(ctx context.Context, keyID int64, period string) (limit, remaining float64, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT limit_rubles, limit_rubles - spent_rubles
		FROM budgets
		WHERE key_id = $1 AND period = $2
	`, keyID, period).Scan(&limit, &remaining)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, ErrBudgetNotFound
		}
		return 0, 0, fmt.Errorf("repo: get budget: %w", err)
	}
	return limit, remaining, nil
}

// exists проверяет, есть ли бюджет для ключа и периода.
func (r *BudgetRepo) exists(ctx context.Context, keyID int64, period string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM budgets WHERE key_id = $1 AND period = $2)
	`, keyID, period).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repo: check budget exists: %w", err)
	}
	return exists, nil
}
