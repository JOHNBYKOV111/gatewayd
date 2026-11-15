package limits

import (
	"sync"
	"testing"
	"time"
)

func TestStore_AllowRPM(t *testing.T) {
	s := NewStore(3, 1000, time.Minute)

	// 3 запроса разрешены.
	for i := 0; i < 3; i++ {
		if !s.AllowRPM("key-1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	// 4-й отклонён.
	if s.AllowRPM("key-1") {
		t.Fatal("4th request should be denied")
	}
}

func TestStore_AllowTPM(t *testing.T) {
	s := NewStore(100, 1000, time.Minute)

	// 1000 токенов — это ровно лимит.
	if !s.AllowTPM("key-1", 1000) {
		t.Fatal("AllowTPM(1000) should be allowed")
	}
	// Ещё 1 токен — отклонён.
	if s.AllowTPM("key-1", 1) {
		t.Fatal("AllowTPM(1) should be denied after limit reached")
	}
}

func TestStore_PerKeyIsolation(t *testing.T) {
	s := NewStore(1, 1000, time.Minute)

	// key-1 исчерпан.
	if !s.AllowRPM("key-1") {
		t.Fatal("key-1 first request should be allowed")
	}
	if s.AllowRPM("key-1") {
		t.Fatal("key-1 second request should be denied")
	}

	// key-2 ещё работает (свой бакет).
	if !s.AllowRPM("key-2") {
		t.Fatal("key-2 first request should be allowed (separate bucket)")
	}
}

func TestStore_LazyInit(t *testing.T) {
	s := NewStore(60, 100000, time.Minute)

	// До первого запроса — 0 ключей.
	if got := s.Size(); got != 0 {
		t.Errorf("initial Size = %d, want 0", got)
	}

	// После запроса — 1 ключ.
	s.AllowRPM("key-1")
	if got := s.Size(); got != 1 {
		t.Errorf("Size after 1 key = %d, want 1", got)
	}

	// Повторный запрос не создаёт новый ключ.
	s.AllowRPM("key-1")
	if got := s.Size(); got != 1 {
		t.Errorf("Size after repeat = %d, want 1 (no duplicate)", got)
	}

	// Новый ключ — Size растёт.
	s.AllowRPM("key-2")
	if got := s.Size(); got != 2 {
		t.Errorf("Size after 2 keys = %d, want 2", got)
	}
}

func TestStore_Reset(t *testing.T) {
	s := NewStore(1, 1000, time.Minute)

	// Исчерпываем key-1.
	s.AllowRPM("key-1")
	if s.AllowRPM("key-1") {
		t.Fatal("key-1 should be denied before Reset")
	}

	// Reset очищает всё.
	s.Reset()
	if got := s.Size(); got != 0 {
		t.Errorf("Size after Reset = %d, want 0", got)
	}

	// После Reset key-1 снова работает.
	if !s.AllowRPM("key-1") {
		t.Fatal("key-1 should be allowed after Reset")
	}
}

func TestStore_Concurrent(t *testing.T) {
	s := NewStore(1000, 100000, time.Minute)

	// 100 горутин одновременно пытаются получить лимит.
	// Из них 1000 запросов разрешены, остальные — отклонены.
	var wg sync.WaitGroup
	allowed := make([]int, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if s.AllowRPM("shared-key") {
					allowed[idx]++
				}
			}
		}(i)
	}
	wg.Wait()

	total := 0
	for _, a := range allowed {
		total += a
	}
	if total != 1000 {
		t.Errorf("total allowed = %d, want 1000 (no lost updates)", total)
	}
}
