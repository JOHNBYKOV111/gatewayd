package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrKeyNotFound = errors.New("repo: key not found")

type Key struct {
	ID            int64
	KeyHash       string
	KeyPrefix     string
	KeyLast4      string
	Owner         string
	AllowedModels []string
	RPMLimit      int
	TPMLimit      int
	CreatedAt     time.Time
	RevokedAt     *time.Time
}

type KeyRepo struct {
	pool *pgxpool.Pool
}

func NewKeyRepo(pool *pgxpool.Pool) *KeyRepo {
	return &KeyRepo{pool: pool}
}

func (r *KeyRepo) Create(ctx context.Context, k Key) (int64, error) {
	// pgx отправляет nil как NULL, а колонка allowed_models — NOT NULL.
	// Поэтому nil нормализуем в пустой массив.
	allowed := k.AllowedModels
	if allowed == nil {
		allowed = []string{}
	}

	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO keys (key_hash, key_prefix, key_last4, owner, allowed_models, rpm_limit, tpm_limit)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, k.KeyHash, k.KeyPrefix, k.KeyLast4, k.Owner, allowed, k.RPMLimit, k.TPMLimit).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("repo: create key: %w", err)
	}
	return id, nil
}

func (r *KeyRepo) FindByHash(ctx context.Context, hash string) (*Key, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, key_hash, key_prefix, key_last4, owner, allowed_models, rpm_limit, tpm_limit, created_at, revoked_at
		FROM keys
		WHERE key_hash = $1 AND revoked_at IS NULL
	`, hash)

	var k Key
	err := row.Scan(&k.ID, &k.KeyHash, &k.KeyPrefix, &k.KeyLast4, &k.Owner, &k.AllowedModels, &k.RPMLimit, &k.TPMLimit, &k.CreatedAt, &k.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("repo: find key: %w", err)
	}
	return &k, nil
}

func (r *KeyRepo) List(ctx context.Context) ([]Key, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, key_hash, key_prefix, key_last4, owner, allowed_models, rpm_limit, tpm_limit, created_at, revoked_at
		FROM keys
		WHERE revoked_at IS NULL
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("repo: list keys: %w", err)
	}
	defer rows.Close()

	var out []Key
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.KeyHash, &k.KeyPrefix, &k.KeyLast4, &k.Owner, &k.AllowedModels, &k.RPMLimit, &k.TPMLimit, &k.CreatedAt, &k.RevokedAt); err != nil {
			return nil, fmt.Errorf("repo: scan key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *KeyRepo) Revoke(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE keys SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("repo: revoke key: %w", err)
	}
	return nil
}
