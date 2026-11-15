package stream

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func readAllChunks(t *testing.T, s Stream) []Event {
	t.Helper()
	var events []Event
	for {
		ev, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("unexpected stream error: %v", err)
		}
		events = append(events, ev)
	}
	return events
}

func TestReader_ValidOllamaFixture(t *testing.T) {
	src := mustOpenFixture(t, "ollama_simple.txt")
	r := NewReader(src)

	events := readAllChunks(t, r)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Data != `{"id":"1","choices":[{"delta":{"content":"Hello"}}]}` {
		t.Errorf("event[0] mismatch: %q", events[0].Data)
	}
	if events[1].Data != `{"id":"1","choices":[{"delta":{"content":" world"}}]}` {
		t.Errorf("event[1] mismatch: %q", events[1].Data)
	}
	if events[2].Data != `{"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}` {
		t.Errorf("event[2] mismatch: %q", events[2].Data)
	}
}

func TestReader_StopsAtDone(t *testing.T) {
	src := strings.NewReader(
		"data: {\"a\":1}\n\n" +
			"data: [DONE]\n\n" +
			"data: {\"b\":2}\n\n",
	)
	r := NewReader(src)

	events := readAllChunks(t, r)

	if len(events) != 1 {
		t.Fatalf("expected 1 event before [DONE], got %d", len(events))
	}
	if events[0].Data != `{"a":1}` {
		t.Errorf("event[0] mismatch: %q", events[0].Data)
	}
}

func TestReader_IgnoresCommentsAndEmptyLines(t *testing.T) {
	src := strings.NewReader(
		": heartbeat\n\n" +
			"data: {\"a\":1}\n\n" +
			"\n" +
			"data: [DONE]\n\n",
	)
	r := NewReader(src)

	events := readAllChunks(t, r)

	if len(events) != 1 || events[0].Data != `{"a":1}` {
		t.Fatalf("expected exactly one event, got %+v", events)
	}
}

func TestReader_MultilineData(t *testing.T) {
	src := strings.NewReader(
		"data: {\"part\":\ndata: \"one\"}\n\n" +
			"data: [DONE]\n\n",
	)
	r := NewReader(src)

	events := readAllChunks(t, r)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data != "{\"part\":\n\"one\"}" {
		t.Errorf("multiline join mismatch: %q", events[0].Data)
	}
}

// TestReader_EOFWithoutDone: поток закончился без [DONE], но с валидными data-чанками.
// Оба события должны прийти, затем io.EOF.
func TestReader_EOFWithoutDone(t *testing.T) {
	src := strings.NewReader(
		"data: {\"a\":1}\n\n" +
			"data: {\"b\":2}\n",
	)
	r := NewReader(src)

	events := readAllChunks(t, r)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
}

// TestReader_PartialLine: поток оборвался посреди data-строки.
// Это не должно паниковать; допустимо либо вернуть частичное событие, либо io.EOF.
func TestReader_PartialLine(t *testing.T) {
	src := strings.NewReader(`data: {"a":`)
	r := NewReader(src)

	// Главное требование: не паниковать и не зависать.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := r.Next(); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("reader hung on partial line")
	}
}

// TestReader_ContextCancel: отмена контекста во время блокирующего чтения
// должна разблокировать Next и вернуть context.Canceled.
func TestReader_ContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	r := NewReaderWithContext(ctx, pr)

	errc := make(chan error, 1)
	go func() {
		_, err := r.Next()
		errc <- err
	}()

	// Дать ридеру немного времени войти в блокирующее чтение.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected error after context cancellation, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Next did not unblock after context cancellation")
	}
}

// TestReader_SlowStream: первое событие должно вернуться до того,
// как писатель отправит второе. Никаких абсолютных порогов в миллисекундах.
func TestReader_SlowStream(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	r := NewReader(pr)

	secondWritten := make(chan struct{})

	go func() {
		_, _ = pw.Write([]byte("data: {\"a\":1}\n\n"))
		// Ждём, пока тест прочитает первое событие, и только потом пишем второе.
		<-secondWritten
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
	}()

	ev, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.Data != `{"a":1}` {
		t.Errorf("first event mismatch: %q", ev.Data)
	}
	close(secondWritten)
}
