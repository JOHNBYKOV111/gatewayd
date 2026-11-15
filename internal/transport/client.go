package transport

import (
	"net"
	"net/http"
	"time"
)

// Timeouts — таймауты, которые мы задаём на транспорт.
// Вынесены в структуру, чтобы в тестах и на П19 (graceful shutdown)
// можно было переопределять.
type Timeouts struct {
	Dial           time.Duration
	TLSHandshake   time.Duration
	ResponseHeader time.Duration
	IdleConn       time.Duration
	Total          time.Duration
}

// DefaultTimeouts — продакшн-значения для LLM-провайдеров.
// Total — большой, потому что стриминг может длиться минутами.
// Ограничение длительности стрима делает routing/timeout, а не транспорт.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Dial:           5 * time.Second,
		TLSHandshake:   5 * time.Second,
		ResponseHeader: 300 * time.Second, // Увеличено для Ollama на CPU (может думать долго)
		IdleConn:       90 * time.Second,
		Total:          0, // без общего таймаута; стрим контролируется ctx
	}
}

// NewClient создаёт *http.Client с настроенным транспортом.
// Возвращаемый клиент безопасен для конкурентного использования.
func NewClient(t Timeouts) *http.Client {
	dialer := &net.Dialer{
		Timeout:   t.Dial,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   t.TLSHandshake,
		ResponseHeaderTimeout: t.ResponseHeader,
		IdleConnTimeout:       t.IdleConn,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   t.Total,
	}
}