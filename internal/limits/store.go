package limits

import (
	"sync"
	"time"
)

// KeyLimits — пара бакетов для одного ключа: RPM и TPM.
type KeyLimits struct {
	RPM *Bucket
	TPM *Bucket
}

// Store — in-memory хранилище лимитов per key.
//
// Осознанное ограничение (см. AGENTS.md и README):
//   - счётчики живут в памяти и сбрасываются при рестарте шлюза;
//   - при двух репликах за балансировщиком счётчики независимы,
//     эффективный RPM удваивается;
//   - Redis / распределённые лимиты — вне скоупа v1.0.
type Store struct {
	mu     sync.RWMutex
	limits map[string]*KeyLimits // key_hash → лимиты

	rpmLimit int
	tpmLimit int
	interval time.Duration
}

// NewStore создаёт хранилище лимитов.
// rpm — лимит запросов за interval, tpm — лимит токенов за interval.
func NewStore(rpm, tpm int, interval time.Duration) *Store {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Store{
		limits:   make(map[string]*KeyLimits),
		rpmLimit: rpm,
		tpmLimit: tpm,
		interval: interval,
	}
}

// For возвращает бакеты для ключа, создавая их при первом обращении.
// Ленивая инициализация: не тратим память на ключи, которые не делали запросов.
func (s *Store) For(keyHash string) *KeyLimits {
	// Быстрый путь: бакеты уже есть (read-lock).
	s.mu.RLock()
	kl, ok := s.limits[keyHash]
	s.mu.RUnlock()
	if ok {
		return kl
	}

	// Медленный путь: создаём под write-lock.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check: пока ждали lock, другой поток мог создать бакеты.
	if kl, ok := s.limits[keyHash]; ok {
		return kl
	}
	kl = &KeyLimits{
		RPM: NewBucket(s.rpmLimit, s.interval),
		TPM: NewBucket(s.tpmLimit, s.interval),
	}
	s.limits[keyHash] = kl
	return kl
}

// AllowRPM проверяет и списывает 1 запрос для ключа.
// Возвращает true, если запрос разрешён.
func (s *Store) AllowRPM(keyHash string) bool {
	return s.For(keyHash).RPM.Allow()
}

// AllowTPM проверяет и списывает n токенов для ключа.
// Возвращает true, если токенов достаточно.
func (s *Store) AllowTPM(keyHash string, n int) bool {
	return s.For(keyHash).TPM.AllowN(n)
}

// Reset очищает все счётчики (полезно для тестов).
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = make(map[string]*KeyLimits)
}

// Size возвращает количество ключей с активными лимитами (для метрик и тестов).
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.limits)
}
