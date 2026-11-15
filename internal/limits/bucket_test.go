package limits

import (
	"testing"
	"time"
)

func TestBucket_AllowsUpToLimit(t *testing.T) {
	// 3 запроса в секунду, ёмкость 3.
	b := NewBucket(3, time.Second)

	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	// 4-й должен быть отклонён (бакет исчерпан).
	if b.Allow() {
		t.Fatal("4th request should be denied (bucket empty)")
	}
}

func TestBucket_RefillsOverTime(t *testing.T) {
	// 10 запросов в секунду, ёмкость 10.
	b := NewBucket(10, time.Second)

	// Опустошаем бакет.
	for i := 0; i < 10; i++ {
		if !b.Allow() {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if b.Allow() {
		t.Fatal("11th request should be denied")
	}

	// Ждём 200 мс → должно накапать ~2 токена (10/сек * 0.2 = 2).
	time.Sleep(200 * time.Millisecond)

	if !b.Allow() {
		t.Error("after refill: first request should be allowed")
	}
	if !b.Allow() {
		t.Error("after refill: second request should be allowed")
	}
	// Третий, скорее всего, ещё не накапал.
	if b.Allow() {
		t.Error("after refill: third request should be denied (only ~2 tokens refilled)")
	}
}

func TestBucket_ZeroLimitDeniesAll(t *testing.T) {
	b := NewBucket(0, time.Second)
	if b.Allow() {
		t.Error("zero limit should deny all requests")
	}
}

func TestBucket_CapacityDoesNotExceedLimit(t *testing.T) {
	// 5 запросов/сек, ждём долго — бакет не должен накопить больше 5.
	b := NewBucket(5, time.Second)
	time.Sleep(300 * time.Millisecond) // 5/сек * 0.3 = 1.5 токена, но ёмкость 5
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("request %d should be allowed (capacity 5)", i)
		}
	}
	if b.Allow() {
		t.Error("6th request should be denied (capacity is 5)")
	}
}

func TestBucket_TokensUsed(t *testing.T) {
	// 1000 токенов/мин, ёмкость 1000.
	b := NewBucket(1000, time.Minute)

	// Списываем 300 токенов — должно пройти.
	if !b.AllowN(300) {
		t.Fatal("AllowN(300) should be allowed")
	}
	// Списываем ещё 700 — должно пройти (всего 1000).
	if !b.AllowN(700) {
		t.Fatal("AllowN(700) should be allowed (total 1000)")
	}
	// Списываем ещё 1 — должно быть отклонено (бакет пуст).
	if b.AllowN(1) {
		t.Fatal("AllowN(1) should be denied (bucket empty)")
	}
}
