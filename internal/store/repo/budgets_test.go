package repo

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func testBudgetPool(t *testing.T) *store.Store {
	t.Helper()
	dsn := testutil.StartPostgres(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := store.Migrate(dsn, "../migrations"); err != nil {
		st.Close()
		t.Fatalf("store.Migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// createTestKey создаёт ключ и возвращает его ID.
func createTestKey(t *testing.T, r *KeyRepo, owner string) int64 {
	t.Helper()
	id, err := r.Create(context.Background(), Key{
		KeyHash:       "hash-" + owner,
		KeyPrefix:     "gw-",
		KeyLast4:      "test",
		Owner:         owner,
		AllowedModels: []string{},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create key: %v", err)
	}
	return id
}

func TestBudgetRepo_Spend_Success(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-spend-success")

	if err := budgetRepo.Set(ctx, keyID, "daily", 10.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}

	remaining, err := budgetRepo.Spend(ctx, keyID, "daily", 3.0)
	if err != nil {
		t.Fatalf("Spend: %v", err)
	}
	if remaining != 7.0 {
		t.Errorf("remaining = %v, want 7.0", remaining)
	}

	remaining, err = budgetRepo.Spend(ctx, keyID, "daily", 6.0)
	if err != nil {
		t.Fatalf("Spend: %v", err)
	}
	if remaining != 1.0 {
		t.Errorf("remaining = %v, want 1.0", remaining)
	}
}

func TestBudgetRepo_Spend_ExceedsLimit(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-spend-exceed")

	if err := budgetRepo.Set(ctx, keyID, "daily", 1.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}

	remaining, err := budgetRepo.Spend(ctx, keyID, "daily", 1.0)
	if err != nil {
		t.Fatalf("first Spend: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %v, want 0", remaining)
	}

	_, err = budgetRepo.Spend(ctx, keyID, "daily", 1.0)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("second Spend: expected ErrBudgetExceeded, got %v", err)
	}
}

func TestBudgetRepo_Spend_NoBudget(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-no-budget")

	_, err := budgetRepo.Spend(ctx, keyID, "daily", 1.0)
	if !errors.Is(err, ErrBudgetNotFound) {
		t.Fatalf("expected ErrBudgetNotFound, got %v", err)
	}
}

func TestBudgetRepo_ConcurrentSpend(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-concurrent")
	if err := budgetRepo.Set(ctx, keyID, "daily", 5.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}

	const goroutines = 100
	const amount = 0.1

	var wg sync.WaitGroup
	results := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := budgetRepo.Spend(ctx, keyID, "daily", amount)
			results[idx] = err
		}(i)
	}
	wg.Wait()

	success := 0
	exceeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrBudgetExceeded):
			exceeded++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if success != 50 {
		t.Errorf("success = %d, want 50 (no lost updates / no over-spend)", success)
	}
	if exceeded != 50 {
		t.Errorf("exceeded = %d, want 50", exceeded)
	}

	remaining, err := budgetRepo.Remaining(ctx, keyID, "daily")
	if err != nil {
		t.Fatalf("Remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %v, want 0", remaining)
	}
}

func TestBudgetRepo_PeriodsAreSeparate(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-periods")

	if err := budgetRepo.Set(ctx, keyID, "daily", 10.0); err != nil {
		t.Fatalf("Set daily: %v", err)
	}
	if err := budgetRepo.Set(ctx, keyID, "monthly", 100.0); err != nil {
		t.Fatalf("Set monthly: %v", err)
	}

	if _, err := budgetRepo.Spend(ctx, keyID, "daily", 3.0); err != nil {
		t.Fatalf("Spend daily: %v", err)
	}

	remaining, err := budgetRepo.Remaining(ctx, keyID, "monthly")
	if err != nil {
		t.Fatalf("Remaining monthly: %v", err)
	}
	if remaining != 100.0 {
		t.Errorf("monthly remaining = %v, want 100.0 (separate from daily)", remaining)
	}

	remaining, err = budgetRepo.Remaining(ctx, keyID, "daily")
	if err != nil {
		t.Fatalf("Remaining daily: %v", err)
	}
	if remaining != 7.0 {
		t.Errorf("daily remaining = %v, want 7.0", remaining)
	}
}

// TestBudgetRepo_LimitAndRemaining — новый метод, один SELECT.
func TestBudgetRepo_LimitAndRemaining(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-limit-remaining")

	if err := budgetRepo.Set(ctx, keyID, "daily", 10.0); err != nil {
		t.Fatalf("Set budget: %v", err)
	}

	// Списываем 3 ₽ → остаток 7, лимит 10.
	if _, err := budgetRepo.Spend(ctx, keyID, "daily", 3.0); err != nil {
		t.Fatalf("Spend: %v", err)
	}

	limit, remaining, err := budgetRepo.LimitAndRemaining(ctx, keyID, "daily")
	if err != nil {
		t.Fatalf("LimitAndRemaining: %v", err)
	}
	if limit != 10.0 {
		t.Errorf("limit = %v, want 10.0", limit)
	}
	if remaining != 7.0 {
		t.Errorf("remaining = %v, want 7.0", remaining)
	}
}

// TestBudgetRepo_LimitAndRemaining_NotFound — если бюджета нет.
func TestBudgetRepo_LimitAndRemaining_NotFound(t *testing.T) {
	st := testBudgetPool(t)
	keyRepo := NewKeyRepo(st.Pool)
	budgetRepo := NewBudgetRepo(st.Pool)
	ctx := context.Background()

	keyID := createTestKey(t, keyRepo, "owner-limit-notfound")

	_, _, err := budgetRepo.LimitAndRemaining(ctx, keyID, "daily")
	if !errors.Is(err, ErrBudgetNotFound) {
		t.Fatalf("expected ErrBudgetNotFound, got %v", err)
	}
}
