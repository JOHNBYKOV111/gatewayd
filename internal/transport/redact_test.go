package transport

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestRedactHeaders_SensitiveKeys(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"Authorization", "Bearer sk-secret-123"},
		{"authorization", "Bearer sk-secret-123"},
		{"X-Api-Key", "sk-secret-123"},
		{"x-api-key", "sk-secret-123"},
		{"Api-Key", "sk-secret-123"},
		{"Cookie", "session=abc"},
	}

	for _, tc := range cases {
		h := http.Header{}
		h.Set(tc.key, tc.value)
		
		got := RedactHeaders(h)
		
		if got.Get(tc.key) != "[REDACTED]" {
			t.Errorf("header %q = %q, want [REDACTED]", tc.key, got.Get(tc.key))
		}
	}
}

func TestRedactHeaders_NonSensitiveUntouched(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "gatewayd/test")
	h.Set("X-Request-Id", "abc-123")

	got := RedactHeaders(h)

	if got.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type changed: %q", got.Get("Content-Type"))
	}
	if got.Get("User-Agent") != "gatewayd/test" {
		t.Errorf("User-Agent changed: %q", got.Get("User-Agent"))
	}
	if got.Get("X-Request-Id") != "abc-123" {
		t.Errorf("X-Request-Id changed: %q", got.Get("X-Request-Id"))
	}
}

func TestRedactHeaders_DoesNotMutateOriginal(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret-123")

	_ = RedactHeaders(h)

	if h.Get("Authorization") != "Bearer sk-secret-123" {
		t.Fatal("RedactHeaders mutated the original header map")
	}
}

func TestRedactHeaders_EmptyHeader(t *testing.T) {
	got := RedactHeaders(http.Header{})
	if got == nil {
		t.Fatal("RedactHeaders(nil-ish) returned nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty header, got %v", got)
	}
}

func TestRedaction_NoSecretInLogOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret-123")
	h.Set("Content-Type", "application/json")

	redacted := RedactHeaders(h)
	logger.Info("outgoing request", "headers", redacted)

	out := buf.String()
	if strings.Contains(out, "sk-secret-123") {
		t.Fatalf("secret leaked into log: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] marker in log, got: %s", out)
	}
}
