package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/auth"
	"github.com/johnbykov1989/gatewayd/internal/store"
	"github.com/johnbykov1989/gatewayd/internal/testutil"
)

func testPool(t *testing.T) *store.Store {
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

func TestKeyRepo_CreateFindRevoke(t *testing.T) {
	st := testPool(t)
	r := NewKeyRepo(st.Pool)
	ctx := context.Background()

	// Генерируем ключ и хешируем.
	raw, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash := auth.HashKey(raw)
	prefix, last4 := auth.PrefixAndLast4(raw)

	// Create.
	id, err := r.Create(ctx, Key{
		KeyHash:       hash,
		KeyPrefix:     prefix,
		KeyLast4:      last4,
		Owner:         "test-owner-create",
		AllowedModels: []string{"llama3.2:3b"},
		RPMLimit:      60,
		TPMLimit:      100000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// FindByHash.
	got, err := r.FindByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.Owner != "test-owner-create" {
		t.Errorf("Owner = %q", got.Owner)
	}
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "llama3.2:3b" {
		t.Errorf("AllowedModels = %v", got.AllowedModels)
	}

	// Revoke.
	if err := r.Revoke(ctx, id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// После Revoke FindByHash должен вернуть ErrKeyNotFound.
	_, err = r.FindByHash(ctx, hash)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("after revoke: expected ErrKeyNotFound, got %v", err)
	}
}

func TestKeyRepo_FindByHash_NotFound(t *testing.T) {
	st := testPool(t)
	r := NewKeyRepo(st.Pool)
	ctx := context.Background()

	_, err := r.FindByHash(ctx, "deadbeef00000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestKeyRepo_List(t *testing.T) {
	st := testPool(t)
	r := NewKeyRepo(st.Pool)
	ctx := context.Background()

	// Создаём 2 ключа.
	for i := 0; i < 2; i++ {
		raw, _ := auth.GenerateKey()
		_, err := r.Create(ctx, Key{
			KeyHash:       auth.HashKey(raw),
			KeyPrefix:     auth.KeyPrefix,
			KeyLast4:      "test",
			Owner:         "test-owner-list",
			AllowedModels: []string{},
			RPMLimit:      60,
			TPMLimit:      100000,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// В базе могут быть ключи от других тестов, поэтому проверяем, что наши есть.
	found := 0
	for _, k := range list {
		if k.Owner == "test-owner-list" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected 2 keys for test-owner-list, found %d", found)
	}
}
