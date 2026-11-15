package stream

import (
	"fmt"
	"net/http"
	"strings"
)

// Writer сериализует канонический Stream в SSE-поток,
// совместимый с OpenAI SDK (text/event-stream).
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
	headers bool
	closed  bool
}

func NewWriter(w http.ResponseWriter) *Writer {
	flusher, _ := w.(http.Flusher)
	return &Writer{
		w:       w,
		flusher: flusher,
	}
}

func (w *Writer) Write(ev Event) error {
	if w.closed {
		return fmt.Errorf("sse writer: write after close")
	}

	if !w.headers {
		w.setHeaders()
	}

	var sb strings.Builder
	for _, line := range strings.Split(ev.Data, "\n") {
		sb.WriteString("data: ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	if _, err := w.w.Write([]byte(sb.String())); err != nil {
		return fmt.Errorf("sse writer: write: %w", err)
	}

	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if !w.headers {
		w.setHeaders()
	}

	if _, err := w.w.Write([]byte("data: [DONE]\n\n")); err != nil {
		return fmt.Errorf("sse writer: close: %w", err)
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

func (w *Writer) setHeaders() {
	w.headers = true
	h := w.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.w.WriteHeader(http.StatusOK)
}
