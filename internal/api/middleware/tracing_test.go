package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/johnbykov1989/gatewayd/internal/obs/tracing"
)

// newTestTracer создаёт tracing.Tracer с in-memory экспортером.
func newTestTracer(t *testing.T) (*tracing.Tracer, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tracing.New(tp.Tracer("gatewayd-test")), exp
}

func TestTracing_CreatesRootSpan(t *testing.T) {
	tr, exp := newTestTracer(t)

	handler := Tracing(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := tr.SpanFromContext(r.Context())
		if !span.SpanContext().IsValid() {
			t.Error("span в контексте невалиден")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("ожидался 1 span, получено %d", len(spans))
	}
	if spans[0].Name != "http.request" {
		t.Errorf("имя span: %q, ожидалось %q", spans[0].Name, "http.request")
	}
}

func TestTracing_Attributes(t *testing.T) {
	tr, exp := newTestTracer(t)

	handler := Tracing(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("ожидался 1 span, получено %d", len(spans))
	}

	attrs := make(map[string]string)
	for _, a := range spans[0].Attributes {
		attrs[string(a.Key)] = a.Value.String()
	}
	if attrs["http.method"] != http.MethodGet {
		t.Errorf("http.method: %q, ожидалось %q", attrs["http.method"], http.MethodGet)
	}
	if attrs["http.path"] != "/v1/chat/completions" {
		t.Errorf("http.path: %q, ожидалось %q", attrs["http.path"], "/v1/chat/completions")
	}
}

func TestTracing_NilTracer_NoPanic(t *testing.T) {
	handler := Tracing(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("ожидался 200, получен %d", rec.Code)
	}
}

func TestTracing_PropagatesSpan(t *testing.T) {
	tr, exp := newTestTracer(t)

	handler := Tracing(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tr.Start(r.Context(), "handler.work")
		_ = ctx
		span.SetAttributes(attribute.String("step", "inner"))
		span.End()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("ожидалось 2 span, получено %d", len(spans))
	}

	var rootID, childParentID string
	for _, s := range spans {
		if s.Name == "handler.work" {
			childParentID = s.Parent.SpanID().String()
		} else {
			rootID = s.SpanContext.SpanID().String()
		}
	}
	if rootID == "" || childParentID == "" {
		t.Fatal("не найден root или вложенный span")
	}
	if rootID != childParentID {
		t.Errorf("родитель вложенного span: %q, ожидался %q", childParentID, rootID)
	}
}

func TestTracing_NoResponseWriterWrapper(t *testing.T) {
	tr, _ := newTestTracer(t)
	var isFlusher bool

	handler := Tracing(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, isFlusher = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !isFlusher {
		t.Error("ResponseWriter потерял интерфейс http.Flusher — SSE сломается")
	}
}

func TestTracing_NoopTracerStillWorks(t *testing.T) {
	tr := tracing.New(noop.NewTracerProvider().Tracer("noop"))
	handler := Tracing(tr)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		_ = span.SpanContext()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("ожидался 200, получен %d", rec.Code)
	}
}
