package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/protocol/openai"
)

// mustReadFixture читает golden-файл из testdata/.
func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestProvider_Do_ConvertResponse(t *testing.T) {
	fixture := mustReadFixture(t, "response_simple.json")

	var receivedBody map[string]any
	var receivedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		receivedHeaders = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	p := New("anthropic", srv.URL, "sk-ant-test", srv.Client())

	req := openai.ChatRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []openai.Message{{Role: "user", Content: "Hi"}},
	}

	resp, err := p.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	// Golden: проверяем, что Anthropic-ответ сконвертирован в OpenAI-формат.
	if resp.ID != "msg_01XFDUDYJgAACzvnptvVoYEL" {
		t.Errorf("ID = %q", resp.ID)
	}
	if resp.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Model = %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("Role = %q", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].Message.Content != "Hello from Anthropic" {
		t.Errorf("Content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop (from end_turn)", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 17 {
		t.Errorf("Usage = %+v, want prompt=12 completion=5 total=17", resp.Usage)
	}

	// Golden: проверяем, что адаптер не исказил запрос.
	if receivedBody["model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("model sent = %v", receivedBody["model"])
	}
	msgs, ok := receivedBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages sent = %v", receivedBody["messages"])
	}

	// Golden: проверяем заголовки Anthropic.
	if receivedHeaders.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("x-api-key = %q", receivedHeaders.Get("x-api-key"))
	}
	if receivedHeaders.Get("anthropic-version") == "" {
		t.Error("anthropic-version header missing")
	}
	if !strings.HasPrefix(receivedHeaders.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q", receivedHeaders.Get("Content-Type"))
	}
}

func TestProvider_HealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic не имеет /models, healthcheck идёт через /v1/messages с минимальным запросом.
		// Но для простоты: проверяем, что HealthCheck не падает, если сервер отвечает 200.
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	p := New("anthropic", srv.URL, "sk-ant-test", srv.Client())
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

// Заглушка для io, чтобы компилятор не ругался на неиспользуемый импорт.
var _ = io.Discard
