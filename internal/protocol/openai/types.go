package openai

import (
	"encoding/json"           // v1: отсюда берём DefaultOptionsV1()
	jsonv2 "encoding/json/v2" // v2: быстрый парсер
)

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func NewErrorResponse(errType, message string) ErrorResponse {
	resp := ErrorResponse{}
	resp.Error.Type = errType
	resp.Error.Message = message
	return resp
}

// ParseChatRequest использует v1-семантику для внешних данных.
// jsonv2.Unmarshal запускает быстрый движок v2,
// а json.DefaultOptionsV1() возвращает мягкое поведение v1
// (прощает дубликаты ключей и невалидный UTF-8) [citation:1][citation:10].
func ParseChatRequest(data []byte) (*ChatRequest, error) {
	var req ChatRequest
	if err := jsonv2.Unmarshal(data, &req, json.DefaultOptionsV1()); err != nil {
		return nil, err
	}
	return &req, nil
}
