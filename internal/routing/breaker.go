package routing

import (
	"errors"
	"sync"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// State — состояние circuit breaker.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker — circuit breaker для одного провайдера.
type Breaker struct {
	mu sync.Mutex

	state            State
	failureThreshold int
	timeout          time.Duration
	halfOpenMaxReqs  int

	failures        int
	successes       int
	lastStateChange time.Time
}

// NewBreaker создаёт breaker с настройками по умолчанию:
// 3 ошибки → open, 30 секунд → half-open, 1 успешный запрос → closed.
func NewBreaker(failureThreshold int, timeout time.Duration) *Breaker {
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Breaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		timeout:          timeout,
		halfOpenMaxReqs:  1,
		lastStateChange:  time.Now(),
	}
}

// Allow проверяет, можно ли отправить запрос через этот breaker.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true

	case StateOpen:
		if time.Since(b.lastStateChange) >= b.timeout {
			b.setState(StateHalfOpen)
			b.successes = 0
			return true
		}
		return false

	case StateHalfOpen:
		if b.successes < b.halfOpenMaxReqs {
			return true
		}
		return false
	}
	return false
}

// RecordSuccess фиксирует успешный запрос.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		b.successes++
		if b.successes >= b.halfOpenMaxReqs {
			b.setState(StateClosed)
			b.failures = 0
		}
	case StateClosed:
		b.failures = 0
	}
}

// RecordFailure фиксирует ошибку.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.setState(StateOpen)
		}
	case StateHalfOpen:
		b.setState(StateOpen)
	}
}

func (b *Breaker) setState(s State) {
	b.state = s
	b.lastStateChange = time.Now()
}

// State возвращает текущее состояние (для метрик).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// NormalizeBreakerError превращает ошибку breaker в каноническую.
func NormalizeBreakerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, provider.ErrProviderUnavailable) {
		return err
	}
	return provider.ErrProviderUnavailable
}
