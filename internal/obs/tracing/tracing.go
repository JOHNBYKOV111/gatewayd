// Package tracing инициализирует OpenTelemetry-трейсинг для gatewayd.
// По умолчанию (если не задан OTEL_EXPORTER_OTLP_ENDPOINT) трейсер работает
// в режиме no-op: спаны создаются, но никуда не экспортируются.
// Это позволяет включать трассировку без внешнего коллектора.
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer — тонкая обёртка над trace.Tracer с nil-safe поведением.
// nil *Tracer безопасен: Start возвращает исходный контекст, End и Shutdown — no-op.
type Tracer struct {
	tracer trace.Tracer
	tp     *sdktrace.TracerProvider
}

// New создаёт Tracer.
//
// Если передан trace.Tracer (например, из тестового провайдера), используется он.
// Если передан nil, поведение зависит от окружения:
//   - OTEL_EXPORTER_OTLP_ENDPOINT задан → инициализируется OTLP/gRPC экспортер.
//   - Не задан → no-op (спаны не экспортируются, но код работает).
func New(custom trace.Tracer) *Tracer {
	if custom != nil {
		return &Tracer{tracer: custom}
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		// No-op провайдер: спаны создаются, но не пишутся никуда.
		return &Tracer{tracer: otel.Tracer("gatewayd")}
	}

	exp, err := otlptracegrpc.New(
		context.Background(),
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		// Fallback в no-op: не валим приложение из-за трейсинга.
		return &Tracer{tracer: otel.Tracer("gatewayd")}
	}

	res, _ := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("gatewayd"),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return &Tracer{tracer: tp.Tracer("gatewayd"), tp: tp}
}

// Start создаёт span и возвращает контекст с ним.
// Если Tracer == nil — возвращает исходный контекст и no-op span.
func (t *Tracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t == nil || t.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return t.tracer.Start(ctx, name, opts...)
}

// SpanFromContext возвращает span из контекста (никогда не nil —
// если span нет, возвращается no-op span).
func (t *Tracer) SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// Shutdown корректно завершает TracerProvider, если он был создан.
// nil-safe: для nil Tracer или no-op провайдера возвращает nil.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.tp == nil {
		return nil
	}
	if err := t.tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("tracing: shutdown: %w", err)
	}
	return nil
}
