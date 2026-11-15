package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey string

const RequestIDKey ctxKey = "request_id"

func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)}))
}

func WithRequestID(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if id, ok := ctx.Value(RequestIDKey).(string); ok && id != "" {
		return logger.With("request_id", id)
	}
	return logger
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
