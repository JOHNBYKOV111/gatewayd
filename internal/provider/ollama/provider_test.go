package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

func fakeOllama(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestProvider_Do_NonStreaming(t *testing.T) {
	var receivedBody map[string]any

	srv := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = jsonv2.UnmarshalRead(r.Body, &receivedBody)

		w.Header().Set("Content-Type", "application/json")
		_ = jsonv2.MarshalWrite(w, map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "qwen2.5:7b",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello from Ollama",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		})
	})

	// ВАЖНО: baseURL включает /v1, как в config.example.yaml
	p := New("local-ollama", srv.URL+"/v1", "test-key", srv.Client())

	req := openai.ChatRequest{
		Model:    "qwen2.5:7b",
		Messages: []openai.Message{{Role: "user", Content: "Hi"}},
	}

	resp, err := p.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if resp.ID != "chatcmpl-1" {
		t.Errorf("ID = %q", resp.ID)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from Ollama" {
		t.Errorf("choices = %+v", resp.Choices)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if receivedBody["model"] != "qwen2.5:7b" {
		t.Errorf("model sent = %v", receivedBody["model"])
	}
}

func TestProvider_Stream_IncludeUsage(t *testing.T) {
	var receivedBody map[string]any

	srv := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		_ = jsonv2.UnmarshalRead(r.Body, &receivedBody)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"id":"1","choices":[{"delta":{"content":"Hello"}}]}`,
			`{"id":"1","choices":[{"delta":{"content":" world"}}]}`,
			`{"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			`[DONE]`,
		}
		for _, c := range chunks {
			if c == "[DONE]" {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			} else {
				_, _ = w.Write([]byte("data: " + c + "\n\n"))
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	p := New("local-ollama", srv.URL+"/v1", "", srv.Client())

	req := openai.ChatRequest{
		Model:    "qwen2.5:7b",
		Messages: []openai.Message{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}

	st, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = st.Close() }()

	so, ok := receivedBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing from request: %+v", receivedBody)
	}
	if so["include_usage"] != true {
		t.Errorf("include_usage = %v, want true", so["include_usage"])
	}

	var lastData string
	for {
		ev, err := st.Next()
		if err != nil {
			break
		}
		lastData = ev.Data
	}

	if !strings.Contains(lastData, `"usage"`) {
		t.Errorf("last event does not contain usage: %q", lastData)
	}
	if !strings.Contains(lastData, `"total_tokens":15`) {
		t.Errorf("usage total_tokens mismatch: %q", lastData)
	}
}

func TestProvider_HealthCheck(t *testing.T) {
	srv := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("healthcheck path = %s, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	p := New("local-ollama", srv.URL+"/v1", "", srv.Client())
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}
