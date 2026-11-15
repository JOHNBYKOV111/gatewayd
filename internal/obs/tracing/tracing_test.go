package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestTracer создаёт Tracer с in-memory экспортером.
// Это стандартный инструмент OTel для тестов (tracetest),
// он позволяет проверять созданные спаны без внешнего коллектора.
func newTestTracer(t *testing.T) (*Tracer, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Передаём tracer из тестового провайдера, чтобы спаны уходили в экспортер.
	return New(tp.Tracer("gatewayd-test")), exp
}

func TestTracer_RequestSpan(t *testing.T) {
	tr, exp := newTestTracer(t)

	ctx := context.Background()
	ctx, span := tr.Start(ctx, "chat")
	span.SetAttributes(
		attribute.Int64("key.id", 42),
		attribute.String("model", "llama3.2:3b"),
	)
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("ожидался 1 span, получено %d", len(spans))
	}
	if spans[0].Name != "chat" {
		t.Errorf("имя span: %q, ожидалось %q", spans[0].Name, "chat")
	}

	attrs := make(map[string]string)
	for _, a := range spans[0].Attributes {
		attrs[string(a.Key)] = a.Value.String()
	}
	if attrs["key.id"] != "42" {
		t.Errorf("атрибут key.id: %q, ожидалось %q", attrs["key.id"], "42")
	}
	if attrs["model"] != "llama3.2:3b" {
		t.Errorf("атрибут model: %q, ожидалось %q", attrs["model"], "llama3.2:3b")
	}

	// Контекст должен содержать span.
	if ctx == context.Background() {
		t.Error("Start должен вернуть новый контекст со span")
	}
}

func TestTracer_NestedProviderAttempt(t *testing.T) {
	tr, exp := newTestTracer(t)

	ctx := context.Background()
	ctx, root := tr.Start(ctx, "chat")
	ctx, attempt := tr.Start(ctx, "provider.attempt")
	attempt.SetAttributes(attribute.String("provider", "ollama"))
	attempt.End()
	root.End()

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("ожидалось 2 span, получено %d", len(spans))
	}

	var rootID, attemptParentID string
	for _, s := range spans {
		switch s.Name {
		case "chat":
			rootID = s.SpanContext.SpanID().String()
		case "provider.attempt":
			attemptParentID = s.Parent.SpanID().String()
		}
	}
	if rootID == "" {
		t.Fatal("не найден root span 'chat'")
	}
	if attemptParentID == "" {
		t.Fatal("не найден вложенный span 'provider.attempt'")
	}
	if rootID != attemptParentID {
		t.Errorf("родитель provider.attempt: %q, ожидался root %q", attemptParentID, rootID)
	}

	// Атрибут provider на вложенном спане.
	var providerAttr string
	for _, s := range spans {
		if s.Name != "provider.attempt" {
			continue
		}
		for _, a := range s.Attributes {
			if string(a.Key) == "provider" {
				providerAttr = a.Value.String()
			}
		}
	}
	if providerAttr != "ollama" {
		t.Errorf("атрибут provider: %q, ожидалось %q", providerAttr, "ollama")
	}

	_ = ctx
}

func TestTracer_NilSafe(t *testing.T) {
	// nil *Tracer должен быть безопасен: Start возвращает исходный контекст,
	// End() и Shutdown() не паникуют.
	var tr *Tracer
	ctx := context.Background()

	newCtx, span := tr.Start(ctx, "test")
	if newCtx != ctx {
		t.Error("nil Start должен вернуть исходный контекст")
	}
	span.End()
	if err := tr.Shutdown(ctx); err != nil {
		t.Errorf("nil Shutdown вернул ошибку: %v", err)
	}
}

func TestTracer_SpanFromContext(t *testing.T) {
	tr, _ := newTestTracer(t)
	ctx := context.Background()

	// Без span в контексте — SpanFromContext возвращает no-op.
	if s := tr.SpanFromContext(ctx); s == nil {
		t.Fatal("SpanFromContext не должен возвращать nil")
	}

	// Со span в контексте — возвращается именно он.
	ctx, span := tr.Start(ctx, "chat")
	if s := tr.SpanFromContext(ctx); s == nil {
		t.Fatal("SpanFromContext должен вернуть span из контекста")
	}
	span.End()
}
