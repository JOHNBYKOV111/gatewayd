package limits

import (
	"sync"
	"time"
)

// Bucket — token bucket для ограничения скорости (RPM/TPM).
// Потокобезопасен: можно вызывать Allow/AllowN из разных горутин.
type Bucket struct {
	mu sync.Mutex

	capacity   float64   // максимальное количество токенов
	refillRate float64   // токенов в секунду
	tokens     float64   // текущее количество токенов
	lastRefill time.Time // когда последний раз пополняли
}

// NewBucket создаёт бакет с ёмкостью `rate` токенов, которая полностью
// восстанавливается за `interval`. Например, NewBucket(60, time.Minute) —
// 60 запросов в минуту (1 токен в секунду).
func NewBucket(rate int, interval time.Duration) *Bucket {
	if rate <= 0 || interval <= 0 {
		return &Bucket{capacity: 0, refillRate: 0, tokens: 0, lastRefill: time.Now()}
	}
	capacity := float64(rate)
	refillPerSec := float64(rate) / interval.Seconds()
	return &Bucket{
		capacity:   capacity,
		refillRate: refillPerSec,
		tokens:     capacity,
		lastRefill: time.Now(),
	}
}

// Allow списывает 1 токен. Возвращает true, если токен был доступен.
func (b *Bucket) Allow() bool {
	return b.AllowN(1)
}

// AllowN списывает n токенов. Возвращает true, если токенов достаточно.
// Если токенов меньше n — ничего не списывает и возвращает false.
func (b *Bucket) AllowN(n int) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.refillRate <= 0 {
		return false
	}

	b.refill()
	need := float64(n)
	if b.tokens < need {
		return false
	}
	b.tokens -= need
	return true
}

// refill пополняет бакет на количество токенов, прошедших с lastRefill.
// Вызывать только под mu.
func (b *Bucket) refill() {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now
}
