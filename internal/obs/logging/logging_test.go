package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNew_Levels(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" debug ", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}
	ctx := context.TODO()
	for _, tc := range cases {
		logger := New(tc.in)
		if !logger.Enabled(ctx, tc.want) {
			t.Errorf("level %q: logger not enabled for %v", tc.in, tc.want)
		}
		if tc.want > slog.LevelDebug && logger.Enabled(ctx, tc.want-1) {
			t.Errorf("level %q: logger unexpectedly enabled below %v", tc.in, tc.want)
		}
	}
}

func TestNew_ReturnsNonNil(t *testing.T) {
	if New("info") == nil {
		t.Fatal("New() = nil")
	}
}
