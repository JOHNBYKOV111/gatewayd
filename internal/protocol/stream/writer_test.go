package stream

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriter_FlushPerChunk(t *testing.T) {
	rr := httptest.NewRecorder()
	w := NewWriter(rr)

	chunks := []string{
		`{"id":"1","choices":[{"delta":{"content":"A"}}]}`,
		`{"id":"1","choices":[{"delta":{"content":"B"}}]}`,
		`{"id":"1","choices":[{"delta":{"content":"C"}}]}`,
		`[DONE]`,
	}

	for _, c := range chunks {
		if err := w.Write(Event{Data: c}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if !rr.Flushed {
			t.Fatalf("expected flush after chunk %q", c)
		}
		rr.Flushed = false
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	body := rr.Body.String()
	for _, c := range chunks {
		if !strings.Contains(body, "data: "+c+"\n\n") {
			t.Errorf("body missing chunk %q:\n%s", c, body)
		}
	}

	if !strings.Contains(body, "data: [DONE]\n\n") {
		t.Errorf("body missing [DONE] terminator:\n%s", body)
	}

	idxA := strings.Index(body, `"content":"A"`)
	idxB := strings.Index(body, `"content":"B"`)
	idxC := strings.Index(body, `"content":"C"`)
	idxDone := strings.Index(body, "[DONE]")

	if idxA > idxB || idxB > idxC || idxC > idxDone {
		t.Errorf("chunks out of order: A=%d B=%d C=%d DONE=%d\n%s", idxA, idxB, idxC, idxDone, body)
	}
}

func TestWriter_Headers(t *testing.T) {
	rr := httptest.NewRecorder()
	w := NewWriter(rr)

	if err := w.Write(Event{Data: `{"a":1}`}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if conn := rr.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}
}

func TestWriter_MultilineData(t *testing.T) {
	rr := httptest.NewRecorder()
	w := NewWriter(rr)

	if err := w.Write(Event{Data: "line1\nline2"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "data: line1\ndata: line2\n\n") {
		t.Errorf("multiline data not serialized correctly:\n%s", body)
	}
}

type failingResponseWriter struct{}

func (f *failingResponseWriter) Header() http.Header { return http.Header{} }
func (f *failingResponseWriter) WriteHeader(_ int)   {}
func (f *failingResponseWriter) Write(_ []byte) (int, error) {
	return 0, fmt.Errorf("client disconnected")
}

func TestWriter_ClientDisconnect(t *testing.T) {
	rr := &failingResponseWriter{}
	w := NewWriter(rr)

	err := w.Write(Event{Data: `{"a":1}`})
	if err == nil {
		t.Fatal("expected error when client disconnected, got nil")
	}
}

func TestWriter_ClosedDouble(t *testing.T) {
	rr := httptest.NewRecorder()
	w := NewWriter(rr)

	_ = w.Write(Event{Data: "x"})
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close should be nil, got: %v", err)
	}
	var b bytes.Buffer
	b.WriteString(rr.Body.String())
	if strings.Count(b.String(), "[DONE]") != 1 {
		t.Errorf("expected exactly one [DONE], got:\n%s", b.String())
	}
}
