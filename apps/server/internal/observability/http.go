package observability

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	httpMetricsOnce sync.Once
	httpCounter     otelmetric.Int64Counter
	httpDuration    otelmetric.Float64Histogram
)

func HTTPMiddleware() func(http.Handler) http.Handler {
	initHTTPMetrics()
	tracer := otel.Tracer("memstation/http")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attrs := []attribute.KeyValue{
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			}
			ctx, span := tracer.Start(
				r.Context(),
				r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
			defer span.End()

			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(recorder, r.WithContext(ctx))

			route := routePattern(r.WithContext(ctx))
			resultAttrs := append(attrs,
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", recorder.statusCode),
			)
			if recorder.statusCode >= http.StatusBadRequest {
				span.SetStatus(codes.Error, http.StatusText(recorder.statusCode))
			}
			span.SetAttributes(resultAttrs...)

			durationMS := float64(time.Since(start).Milliseconds())
			httpCounter.Add(ctx, 1, otelmetric.WithAttributes(resultAttrs...))
			httpDuration.Record(ctx, durationMS, otelmetric.WithAttributes(resultAttrs...))
		})
	}
}

func initHTTPMetrics() {
	httpMetricsOnce.Do(func() {
		meter := otel.Meter("memstation/http")
		httpCounter, _ = meter.Int64Counter("memstation.http.server.requests")
		httpDuration, _ = meter.Float64Histogram("memstation.http.server.duration_ms")
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusRecorder) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func routePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	if r.URL.Path != "" {
		return r.URL.Path
	}
	return "/"
}

func TraceSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("memstation/service").Start(ctx, name, trace.WithAttributes(attrs...))
}

func IntAttr(key string, value int) attribute.KeyValue {
	return attribute.Int(key, value)
}
