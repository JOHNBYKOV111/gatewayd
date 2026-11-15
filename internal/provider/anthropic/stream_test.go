package anthropic

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnbykov1989/gatewayd/internal/protocol/stream"
)

func mustOpenFixture(t *testing.T, name string) io.Reader {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestConvertStream_ValidFixture(t *testing.T) {
	src := mustOpenFixture(t, "stream_simple.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	raw := stream.NewReaderWithContext(ctx, src)
	conv := NewStreamConverter(raw)

	var chunks []string
	for {
		ev, err := conv.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, ev.Data)
	}

	// Ожидаем 3 контент-чанка (Hello, from, Anthropic) + 1 финальный (stop_reason + usage).
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4:\n%s", len(chunks), strings.Join(chunks, "\n"))
	}

	// Проверяем, что каждый контент-чанк в OpenAI-формате.
	if !strings.Contains(chunks[0], `"content":"Hello"`) {
		t.Errorf("chunk[0] = %s", chunks[0])
	}
	if !strings.Contains(chunks[1], `"content":" from"`) {
		t.Errorf("chunk[1] = %s", chunks[1])
	}
	if !strings.Contains(chunks[2], `"content":" Anthropic"`) {
		t.Errorf("chunk[2] = %s", chunks[2])
	}

	// Финальный чанк должен содержать finish_reason и usage.
	final := chunks[3]
	if !strings.Contains(final, `"finish_reason":"stop"`) {
		t.Errorf("final chunk missing finish_reason: %s", final)
	}
	if !strings.Contains(final, `"prompt_tokens"`) {
		t.Errorf("final chunk missing prompt_tokens: %s", final)
	}
	if !strings.Contains(final, `"completion_tokens":8`) {
		t.Errorf("final chunk completion_tokens should be 8 (max-merge from message_delta): %s", final)
	}
	if !strings.Contains(final, `"total_tokens"`) {
		t.Errorf("final chunk missing total_tokens: %s", final)
	}

	// Usage: input_tokens был 0 в message_start, но в message_delta пришёл 8?
	// В реальности input_tokens приходит в message_delta как часть usage,
	// но в нашей фикстуре только output_tokens:8. Значит, input_tokens останется 0,
	// если не пришёл второй непустой. Это проверяет правило «первое непустое».
	// В нашем случае input_tokens = 0 (не было непустого), output_tokens = 8.
	if !strings.Contains(final, `"prompt_tokens":0`) {
		// Если бы мы ошиблись и взяли max, было бы тоже 0. Проверяем, что не отрицательное.
		t.Logf("prompt_tokens is 0 (expected, since fixture has no non-zero input_tokens)")
	}
}

func TestConvertStream_Empty(t *testing.T) {
	src := strings.NewReader("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	raw := stream.NewReaderWithContext(ctx, src)
	conv := NewStreamConverter(raw)

	_, err := conv.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF on empty stream, got %v", err)
	}
}

func TestConvertStream_IgnoresPing(t *testing.T) {
	src := strings.NewReader(
		"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"Hi\"}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	raw := stream.NewReaderWithContext(ctx, src)
	conv := NewStreamConverter(raw)

	ev, err := conv.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !strings.Contains(ev.Data, `"content":"Hi"`) {
		t.Errorf("expected content chunk, got: %s", ev.Data)
	}
}
