package transport

import (
	"bytes"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// RetryConfig настраивает поведение retry-транспорта.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}
}

type retryTransport struct {
	base   http.RoundTripper
	config RetryConfig
}

// NewRetryTransport оборачивает базовый транспорт логикой повторов.
func NewRetryTransport(base http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{base: base, config: cfg}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	var err error

	// Читаем тело запроса один раз, чтобы иметь возможность его переотправить.
	if req.Body != nil {
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt < t.config.MaxAttempts; attempt++ {
		// Пересоздаём тело для каждой попытки.
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := t.base.RoundTrip(req)
		lastResp, lastErr = resp, err

		// Если ошибка сети (таймаут, connection reset) — пробуем ещё.
		if err != nil {
			if attempt < t.config.MaxAttempts-1 {
				t.sleep(attempt)
				continue
			}
			return nil, provider.NormalizeError(err)
		}

		// Если ответ успешный или не 429/5xx — возвращаем как есть.
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}

		// Если это последняя попытка — возвращаем ответ как есть (клиент сам разберётся).
		if attempt == t.config.MaxAttempts-1 {
			return resp, nil
		}

		// Закрываем тело, чтобы не утекало, и ждём перед следующей попыткой.
		_ = resp.Body.Close()
		t.sleep(attempt)
	}

	return lastResp, lastErr
}

func (t *retryTransport) sleep(attempt int) {
	delay := t.config.BaseDelay * time.Duration(1<<attempt)
	if delay > t.config.MaxDelay {
		delay = t.config.MaxDelay
	}
	// Джиттер: ±25% от задержки.
	jitter := time.Duration(rand.Int63n(int64(delay) / 2))
	time.Sleep(delay - delay/4 + jitter)
}
