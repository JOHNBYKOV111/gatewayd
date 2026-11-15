package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/johnbykov1989/gatewayd/internal/api/middleware"
	"github.com/johnbykov1989/gatewayd/internal/cache"
	"github.com/johnbykov1989/gatewayd/internal/config"
	"github.com/johnbykov1989/gatewayd/internal/obs/metrics"
	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/routing"
	"github.com/johnbykov1989/gatewayd/internal/store/repo"
	"github.com/johnbykov1989/gatewayd/internal/usage"
)

const maxBodyBytes = 10 << 20 // 10 MB

// ChatHandler — зависимости для хендлера chat completions.
type ChatHandler struct {
	logger   *slog.Logger
	router   *routing.Router
	callLogs *repo.CallLogRepo
	cache    *cache.Cache
	cfg      *config.Config
	logCh    chan repo.CallLog
	m        *metrics.Metrics
	tr       *tracing.Tracer
}

// NewChatCompletions создаёт хендлер с асинхронной записью журнала и exact-кэшем.
// Ошибка БД при записи журнала не ломает проксирование (некритичный путь).
func NewChatCompletions(
	logger *slog.Logger,
	router *routing.Router,
	callLogs *repo.CallLogRepo,
	cfg *config.Config,
	exactCache *cache.Cache,
	m *metrics.Metrics,
	tr *tracing.Tracer,
) *ChatHandler {
	h := &ChatHandler{
		logger:   logger,
		router:   router,
		callLogs: callLogs,
		cache:    exactCache,
		cfg:      cfg,
		logCh:    make(chan repo.CallLog, 1024),
		m:        m,
		tr:       tr,
	}
	if callLogs != nil {
		go h.logWorker()
	}
	return h
}

// Handle — HTTP-хендлер. Совместим с http.HandlerFunc.
func (h *ChatHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "content-type must be application/json")
		h.incRequests("", "", "400")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		h.incRequests("", "", "400")
		return
	}

	req, err := openai.ParseChatRequest(body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		h.incRequests("", "", "400")
		return
	}

	if req.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		h.incRequests("", "", "400")
		return
	}
	if len(req.Messages) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		h.incRequests("", "", "400")
		return
	}

	h.logger.Info("chat completion request received",
		"model", req.Model,
		"messages_count", len(req.Messages),
		"stream", req.Stream,
	)

	key, _ := middleware.KeyFromContext(r.Context())
	promptHash := hashMessages(req.Messages)
	paramsHash := hashParams(req)

	// Устанавливаем бизнес-атрибуты на root-span (создан middleware.Tracing).
	// Это безопасно: если span отсутствует (no-op), SetAttributes ничего не делает.
	span := trace.SpanFromContext(r.Context())
	if key != nil {
		span.SetAttributes(attribute.Int64("key.id", key.ID))
	}
	span.SetAttributes(attribute.String("model", req.Model))

	cacheKey := cache.Key{
		Model:      req.Model,
		PromptHash: promptHash,
		ParamsHash: paramsHash,
	}

	start := time.Now()

	// 1. Проверка кэша.
	if h.cache != nil {
		if req.Stream {
			if chunks, ok := h.cache.GetStream(cacheKey); ok {
				h.logger.Info("cache hit (stream)", "model", req.Model)
				h.writeCachedStream(w, chunks)
				latency := time.Since(start)
				h.enqueueLog(key, req, promptHash, "cache", 0, 0, 0, 0, latency, latency, "")
				h.incRequests("cache", req.Model, "200")
				h.setCacheHitRatio(h.cache.HitRatio())
				return
			}
		} else {
			if resp, ok := h.cache.Get(cacheKey); ok {
				h.logger.Info("cache hit", "model", req.Model)
				latency := time.Since(start)
				tokens := usage.ExtractTokens(*resp)
				cost := h.calculateCost(req.Model, tokens)
				h.writeCachedJSON(w, resp)
				h.enqueueLog(key, req, promptHash, "cache", tokens.PromptTokens, tokens.CompletionTokens, tokens.TotalTokens, cost, latency, latency, "")
				h.incRequests("cache", req.Model, "200")
				h.setCacheHitRatio(h.cache.HitRatio())
				return
			}
		}
	}

	// 2. Cache miss — идём к провайдеру.
	// Вложенный span на попытку провайдера. Атрибуты key.id и model уже на root,
	// но на попытке полезно видеть provider (какой именно провайдер ответил).
	attemptCtx := r.Context()
	attemptSpan := trace.SpanFromContext(attemptCtx)
	if h.tr != nil {
		attemptCtx, attemptSpan = h.tr.Start(attemptCtx, "provider.attempt")
	}
	defer attemptSpan.End()

	result, err := h.router.Execute(attemptCtx, *req, h.logger)
	if err != nil {
		h.logger.Error("provider error", "err", err, "model", req.Model)
		writeProviderError(w, err)
		h.enqueueLog(key, req, promptHash, "", 0, 0, 0, 0, time.Since(start), 0, err.Error())
		h.incRequests("", req.Model, "502") // статус по ошибке провайдера
		h.incProviderError("", "provider_error")
		return
	}

	// Устанавливаем provider на attempt-span после успешного выбора.
	attemptSpan.SetAttributes(attribute.String("provider", result.Provider()))

	// 3. Пишем ответ клиенту.
	if err := result.Write(w); err != nil {
		h.logger.Error("result write error", "err", err, "model", req.Model)
		h.enqueueLog(key, req, promptHash, result.Provider(), 0, 0, 0, 0, time.Since(start), 0, err.Error())
		h.incRequests(result.Provider(), req.Model, "500")
		return
	}

	// 4. После Write доступны usage, provider, ttft и Raw-данные для кэша.
	tokens := result.Usage()
	cost := h.calculateCost(req.Model, tokens)
	ttft := result.TTFT()

	// Метрики: успешный запрос и стоимость.
	h.incRequests(result.Provider(), req.Model, "200")
	h.addCostRubles(cost)

	// 5. Кэшируем результат.
	if h.cache != nil {
		switch raw := result.Raw().(type) {
		case *openai.ChatResponse:
			h.cache.Set(cacheKey, raw)
		case []string:
			h.cache.SetStream(cacheKey, raw)
		}
		h.setCacheHitRatio(h.cache.HitRatio())
	}

	h.enqueueLog(
		key, req, promptHash, result.Provider(),
		tokens.PromptTokens, tokens.CompletionTokens, tokens.TotalTokens,
		cost,
		time.Since(start), ttft,
		"",
	)
}

func (h *ChatHandler) incRequests(provider, model, status string) {
	if h.m != nil {
		h.m.IncRequests(provider, model, status)
	}
}

func (h *ChatHandler) incProviderError(provider, class string) {
	if h.m != nil {
		h.m.IncProviderError(provider, class)
	}
}

func (h *ChatHandler) addCostRubles(cost float64) {
	if h.m != nil {
		h.m.AddCostRubles(cost)
	}
}

func (h *ChatHandler) setCacheHitRatio(ratio float64) {
	if h.m != nil {
		h.m.SetCacheHitRatio(ratio)
	}
}

// writeCachedJSON отдаёт закэшированный нестриминговый ответ.
func (h *ChatHandler) writeCachedJSON(w http.ResponseWriter, resp *openai.ChatResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeCachedStream отдаёт закэшированные чанки как SSE.
func (h *ChatHandler) writeCachedStream(w http.ResponseWriter, chunks []string) {
	sw := stream.NewWriter(w)
	for _, c := range chunks {
		_ = sw.Write(stream.Event{Data: c})
	}
	_ = sw.Close()
}

// calculateCost находит цену модели в конфиге и считает стоимость.
func (h *ChatHandler) calculateCost(model string, tokens usage.Tokens) float64 {
	if h.cfg == nil {
		return 0
	}
	for _, m := range h.cfg.Models {
		if m.Name == model {
			return usage.CalculateCost(tokens, m.Price)
		}
	}
	return 0
}

// Stop останавливает воркер журнала (вызывать при graceful shutdown).
func (h *ChatHandler) Stop() {
	if h.callLogs != nil {
		close(h.logCh)
	}
}

// enqueueLog ставит запись в очередь на асинхронную запись.
func (h *ChatHandler) enqueueLog(
	key *repo.Key,
	req *openai.ChatRequest,
	promptHash string,
	provider string,
	promptTokens, completionTokens, totalTokens int,
	costRubles float64,
	latency time.Duration,
	ttft time.Duration,
	errMsg string,
) {
	if h.callLogs == nil {
		return
	}

	var keyID *int64
	if key != nil {
		keyID = &key.ID
	}

	cl := repo.CallLog{
		Timestamp:        time.Now().UTC(),
		KeyID:            keyID,
		Model:            req.Model,
		Provider:         provider,
		PromptHash:       promptHash,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		CostRubles:       costRubles,
		LatencyMS:        int(latency.Milliseconds()),
		TTFTMS:           int(ttft.Milliseconds()),
		Error:            errMsg,
	}

	select {
	case h.logCh <- cl:
	default:
		h.logger.Warn("call log queue full, dropping record", "model", req.Model)
	}
}

// logWorker читает очередь и пишет в БД. Ошибки логируются, но не ломают проксирование.
func (h *ChatHandler) logWorker() {
	for cl := range h.logCh {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.callLogs.Record(ctx, cl); err != nil {
			h.logger.Error("call log record failed", "err", err)
		}
		cancel()
	}
}

// hashMessages считает SHA-256 от конкатенации ролей и содержимого сообщений.
func hashMessages(messages []openai.Message) string {
	h := sha256.New()
	for _, m := range messages {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashParams считает SHA-256 от параметров запроса, влияющих на ответ (кроме messages).
func hashParams(req *openai.ChatRequest) string {
	h := sha256.New()
	h.Write([]byte(req.Model))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatBool(req.Stream)))
	h.Write([]byte{0})
	return hex.EncodeToString(h.Sum(nil))
}
