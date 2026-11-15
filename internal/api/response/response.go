package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/usage"
)

// Result — канонический результат выполнения запроса.
// Инкапсулирует логику сериализации (JSON или SSE) и метаданные
// (провайдер, usage, TTFT) для журнала вызовов.
type Result interface {
	Write(w http.ResponseWriter) error
	Provider() string
	Usage() usage.Tokens
	// TTFT — time to first token. Для нестрима — общая latency ответа,
	// для стрима — время до первого чанка. Заполняется после Write.
	TTFT() time.Duration
	// Raw возвращает «сырые» данные для кэша: *openai.ChatResponse (нестрим)
	// или []string (стрим, буферизованные чанки). nil, если кэшировать нечего.
	Raw() any
}

// JSONResult — обычный (нестриминговый) ответ.
type JSONResult struct {
	Resp     openai.ChatResponse
	provider string
	ttft     time.Duration
}

// NewJSONResult создаёт Result для нестримингового ответа.
// provider — имя провайдера, который реально ответил (для журнала).
func NewJSONResult(resp openai.ChatResponse, provider string) *JSONResult {
	return &JSONResult{Resp: resp, provider: provider}
}

func (r *JSONResult) Write(w http.ResponseWriter) error {
	start := time.Now()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(r.Resp); err != nil {
		return fmt.Errorf("response: encode json: %w", err)
	}
	// Для нестрима TTFT = время на запись всего ответа.
	r.ttft = time.Since(start)
	return nil
}

func (r *JSONResult) Provider() string {
	return r.provider
}

func (r *JSONResult) Usage() usage.Tokens {
	if r.Resp.Usage == nil {
		return usage.Tokens{}
	}
	return usage.Tokens{
		PromptTokens:     r.Resp.Usage.PromptTokens,
		CompletionTokens: r.Resp.Usage.CompletionTokens,
		TotalTokens:      r.Resp.Usage.TotalTokens,
	}
}

func (r *JSONResult) TTFT() time.Duration {
	return r.ttft
}

// Raw возвращает указатель на ответ для кэша (нестрим).
func (r *JSONResult) Raw() any {
	return &r.Resp
}

// StreamResult — потоковый ответ (SSE).
//
// Usage и Provider известны только после завершения потока (usage приходит
// в финальном чанке). TTFT — время до первого чанка. Всё заполняется в Write.
// Чанки буферизуются в chunks для последующего кэширования (Raw).
type StreamResult struct {
	Stream   stream.Stream
	Logger   *slog.Logger
	provider string

	// Заполняется в Write.
	usage  usage.Tokens
	ttft   time.Duration
	chunks []string
}

// NewStreamResult создаёт Result для стримингового ответа.
func NewStreamResult(st stream.Stream, logger *slog.Logger, provider string) *StreamResult {
	return &StreamResult{Stream: st, Logger: logger, provider: provider}
}

func (r *StreamResult) Write(w http.ResponseWriter) error {
	defer func() { _ = r.Stream.Close() }()

	start := time.Now()
	sw := stream.NewWriter(w)
	firstChunk := true

	for {
		ev, err := r.Stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return sw.Close()
			}
			if r.Logger != nil {
				r.Logger.Warn("stream interrupted", "err", err)
			}
			return err
		}

		// Замеряем TTFT на первом чанке.
		if firstChunk {
			r.ttft = time.Since(start)
			firstChunk = false
		}

		// Буферизуем чанк для кэша (Raw).
		r.chunks = append(r.chunks, ev.Data)

		// Пытаемся извлечь usage из чанка (OpenAI-совместимые провайдеры
		// присылают usage в финальном чанке при include_usage=true).
		r.extractUsage(ev.Data)

		if err := sw.Write(ev); err != nil {
			if r.Logger != nil {
				r.Logger.Warn("client write error", "err", err)
			}
			return err
		}
	}
}

func (r *StreamResult) Provider() string {
	return r.provider
}

func (r *StreamResult) Usage() usage.Tokens {
	return r.usage
}

func (r *StreamResult) TTFT() time.Duration {
	return r.ttft
}

// Raw возвращает буферизованные чанки для кэша (стрим).
// nil, если чанков нет (стрим не дошёл до первого чанка).
func (r *StreamResult) Raw() any {
	if len(r.chunks) == 0 {
		return nil
	}
	return r.chunks
}

// extractUsage парсит чанк и, если в нём есть usage, обновляет r.usage.
// Ошибки парсинга игнорируются: не все чанки содержат usage.
func (r *StreamResult) extractUsage(data string) {
	if !containsUsage(data) {
		return
	}

	var chunk struct {
		Usage *openai.Usage `json:"usage"`
	}
	if err := jsonv2.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.Usage == nil {
		return
	}
	r.usage = usage.Tokens{
		PromptTokens:     chunk.Usage.PromptTokens,
		CompletionTokens: chunk.Usage.CompletionTokens,
		TotalTokens:      chunk.Usage.TotalTokens,
	}
}

func containsUsage(data string) bool {
	const marker = `"usage"`
	for i := 0; i+len(marker) <= len(data); i++ {
		if data[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}
