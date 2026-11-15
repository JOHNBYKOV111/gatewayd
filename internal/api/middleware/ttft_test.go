package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
)

func TestTTFT_MeasuresOnFirstWrite(t *testing.T) {
	m := metrics.New()
	handler := TTFT(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Имитируем стрим: пишем заголовки, затем первый чанк, затем ещё.
		w.WriteHeader(http.StatusOK)
		time.Sleep(20 * time.Millisecond) // искусственная задержка "провайдера"
		if _, err := w.Write([]byte("data: chunk1\n\n")); err != nil {
			t.Fatalf("write chunk1: %v", err)
		}
		if _, err := w.Write([]byte("data: chunk2\n\n")); err != nil {
			t.Fatalf("write chunk2: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(WithStreamFlag(req.Context()))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// TTFT должен быть записан ровно один раз со значением > 0.
	// Проверяем гистограмму через её DTO (Data Transfer Object).
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "gw_ttft_seconds" {
			found = true
			hist := mf.GetMetric()[0].GetHistogram()
			if hist.GetSampleCount() != 1 {
				t.Errorf("ожидалось 1 наблюдение TTFT, получено %d", hist.GetSampleCount())
			}
			if hist.GetSampleSum() < 0.02 {
				t.Errorf("ожидалась сумма TTFT >= 0.02s, получено %f", hist.GetSampleSum())
			}
		}
	}
	if !found {
		t.Error("gw_ttft_seconds не найдена в реестре")
	}
}

func TestTTFT_IgnoresNonStream(t *testing.T) {
	m := metrics.New()
	handler := TTFT(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"result":"ok"}`)); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}))

	// Здесь нет WithStreamFlag — это обычный JSON-ответ.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Гистограмма должна иметь ноль наблюдений.
	mfs, _ := m.Registry().Gather()
	for _, mf := range mfs {
		if mf.GetName() == "gw_ttft_seconds" {
			if count := mf.GetMetric()[0].GetHistogram().GetSampleCount(); count != 0 {
				t.Errorf("ожидалось 0 наблюдений TTFT для не-стрима, получено %d", count)
			}
		}
	}
}

func TestTTFT_PreservesFlusher(t *testing.T) {
	// Проверяем, что обёртка по-прежнему реализует http.Flusher, чтобы
	// SSE-хендлеры могли вызывать Flush без паники при приведении типа.
	m := metrics.New()
	var flushed bool
	handler := TTFT(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed = true
		} else {
			t.Error("обёрнутый writer не реализует http.Flusher")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req = req.WithContext(WithStreamFlag(req.Context()))
	// httptest.NewRecorder реализует http.Flusher в новых версиях Go.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !flushed {
		t.Error("Flush так и не был вызван на нижележащем writer'е")
	}
}

func TestTTFT_NoStreamFlag_NoFlusherRequirement(t *testing.T) {
	// Не-стрим запрос не должен требовать от нижележащего writer'а
	// реализации Flusher. Это гарантирует, что мы не ломаем обычные хендлеры.
	m := metrics.New()
	handler := TTFT(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("ожидался 200, получен %d", rec.Code)
	}
}

func TestWithStreamFlag(t *testing.T) {
	ctx := context.Background()
	if ctx.Value(StreamContextKey) != nil {
		t.Error("неожиданный флаг стрима в чистом контексте")
	}
	ctx = WithStreamFlag(ctx)
	if ctx.Value(StreamContextKey) != true {
		t.Error("флаг стрима не установлен")
	}
}
