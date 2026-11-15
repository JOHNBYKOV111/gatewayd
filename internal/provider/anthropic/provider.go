package anthropic

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
	"github.com/johnbykov1989/gatewayd/internal/provider"
)

const (
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 4096
)

// Provider реализует provider.Provider для Anthropic Messages API.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

func New(name, baseURL, apiKey string, client *http.Client) *Provider {
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  client,
	}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Chat:       true,
		Streaming:  true,
		Embeddings: false,
		Tools:      true,
		Vision:     true,
	}
}

func (p *Provider) Do(ctx context.Context, req openai.ChatRequest) (openai.ChatResponse, error) {
	body, err := p.buildRequest(req)
	if err != nil {
		return openai.ChatResponse{}, fmt.Errorf("anthropic: build request: %w", err)
	}

	httpReq, err := p.newHTTPRequest(ctx, body, false)
	if err != nil {
		return openai.ChatResponse{}, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return openai.ChatResponse{}, provider.NormalizeError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return openai.ChatResponse{}, p.mapHTTPError(resp)
	}

	var anthResp anthropicResponse
	if err := jsonv2.UnmarshalRead(resp.Body, &anthResp); err != nil {
		return openai.ChatResponse{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return convertResponse(&anthResp), nil
}

func (p *Provider) Stream(ctx context.Context, req openai.ChatRequest) (stream.Stream, error) {
	body, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}

	httpReq, err := p.newHTTPRequest(ctx, body, true)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, p.mapHTTPError(resp)
	}

	// П12: оборачиваем сырой Anthropic SSE в конвертер,
	// который отдаёт OpenAI-совместимые чанки и мержит usage.
	raw := stream.NewReaderWithContext(ctx, resp.Body)
	return NewStreamConverter(raw), nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(req)
	if err != nil {
		return provider.NormalizeError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		return provider.ErrProviderUnavailable
	}
	return nil
}

func (p *Provider) Usage(resp openai.ChatResponse) openai.Usage {
	if resp.Usage == nil {
		return openai.Usage{}
	}
	return *resp.Usage
}

// --- внутренние типы Anthropic ---

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (p *Provider) buildRequest(req openai.ChatRequest) ([]byte, error) {
	ar := anthropicRequest{
		Model:     req.Model,
		MaxTokens: defaultMaxTokens,
		Stream:    req.Stream,
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			ar.System = m.Content
			continue
		}
		ar.Messages = append(ar.Messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	return jsonv2.Marshal(ar)
}

func (p *Provider) newHTTPRequest(ctx context.Context, body []byte, stream bool) (*http.Request, error) {
	url := p.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

func (p *Provider) mapHTTPError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return provider.ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return provider.ErrRateLimited
	case resp.StatusCode >= 500:
		return provider.ErrProviderUnavailable
	default:
		return provider.ErrInvalidRequest
	}
}

func convertResponse(a *anthropicResponse) openai.ChatResponse {
	content := ""
	for _, c := range a.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	finishReason := "stop"
	if a.StopReason == "max_tokens" {
		finishReason = "length"
	}

	return openai.ChatResponse{
		ID:     a.ID,
		Object: "chat.completion",
		Model:  a.Model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Message:      openai.Message{Role: "assistant", Content: content},
				FinishReason: finishReason,
			},
		},
		Usage: &openai.Usage{
			PromptTokens:     a.Usage.InputTokens,
			CompletionTokens: a.Usage.OutputTokens,
			TotalTokens:      a.Usage.InputTokens + a.Usage.OutputTokens,
		},
	}
}
