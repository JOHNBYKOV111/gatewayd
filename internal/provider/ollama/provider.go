package ollama

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

// Provider реализует provider.Provider для Ollama (OpenAI-совместимый эндпоинт).
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

// New создаёт адаптер. apiKey может быть пустым — Ollama не требует ключ,
// но поле оставлено для совместимости с конфигом.
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
		Embeddings: false, // Ollama поддерживает, но в MVP не используем
		Tools:      false,
		Vision:     false,
	}
}

func (p *Provider) Do(ctx context.Context, req openai.ChatRequest) (openai.ChatResponse, error) {
	req.Stream = false
	body, err := p.buildRequest(req)
	if err != nil {
		return openai.ChatResponse{}, fmt.Errorf("ollama: build request: %w", err)
	}

	httpReq, err := p.newHTTPRequest(ctx, body)
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

	var out openai.ChatResponse
	if err := jsonv2.UnmarshalRead(resp.Body, &out); err != nil {
		return openai.ChatResponse{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req openai.ChatRequest) (stream.Stream, error) {
	req.Stream = true
	body, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}

	httpReq, err := p.newHTTPRequest(ctx, body)
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

	return stream.NewReaderWithContext(ctx, resp.Body), nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	url := p.baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return provider.NormalizeError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
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

// buildRequest сериализует запрос, принудительно добавляя stream_options.include_usage.
func (p *Provider) buildRequest(req openai.ChatRequest) ([]byte, error) {
	type ollamaRequest struct {
		openai.ChatRequest
		StreamOptions *streamOptions `json:"stream_options,omitempty"`
	}

	or := ollamaRequest{ChatRequest: req}
	if req.Stream {
		or.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	return jsonv2.Marshal(or)
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (p *Provider) newHTTPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := p.baseURL + "/chat/completions"
	// Используем bytes.NewReader для эффективной передачи тела
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
