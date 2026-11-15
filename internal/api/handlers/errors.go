package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

// writeProviderError превращает ошибку провайдера в HTTP-ответ
// в формате, совместимом с OpenAI SDK.
func writeProviderError(w http.ResponseWriter, err error) {
	status, errType, message := mapProviderError(err)
	writeJSONError(w, status, errType, message)
}

func mapProviderError(err error) (status int, errType, message string) {
	switch {
	case errors.Is(err, provider.ErrUnauthorized):
		return http.StatusUnauthorized, "invalid_request_error", "provider authentication failed"
	case errors.Is(err, provider.ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limit_error", "provider rate limit exceeded"
	case errors.Is(err, provider.ErrTimeout):
		return http.StatusGatewayTimeout, "server_error", "provider request timed out"
	case errors.Is(err, provider.ErrProviderUnavailable):
		return http.StatusBadGateway, "server_error", "provider unavailable"
	case errors.Is(err, provider.ErrInvalidRequest):
		return http.StatusBadRequest, "invalid_request_error", "provider rejected the request"
	case errors.Is(err, context.Canceled):
		return http.StatusBadRequest, "invalid_request_error", "request canceled by client"
	default:
		return http.StatusInternalServerError, "server_error", "internal server error"
	}
}

// writeJSONError — единая точка для всех ошибочных ответов в формате OpenAI.
func writeJSONError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := openai.NewErrorResponse(errType, message)
	_ = json.NewEncoder(w).Encode(resp)
}
