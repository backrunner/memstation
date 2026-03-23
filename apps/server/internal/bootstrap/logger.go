package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func NewLogger(cfg Config) *slog.Logger {
	level := parseLogLevel(cfg.LogLevel)
	jsonHandler := &traceContextHandler{
		next: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}),
	}
	bridgeHandler := &minLevelHandler{
		minLevel: level,
		next: otelslog.NewHandler("memstation/apps/server",
			otelslog.WithVersion(cfg.Version),
			otelslog.WithAttributes(
				attribute.String("service.name", cfg.AppName),
				attribute.String("service.version", cfg.Version),
			),
		),
	}
	handler := &fanoutHandler{handlers: []slog.Handler{jsonHandler, bridgeHandler}}
	return slog.New(handler).With("app", cfg.AppName, "version", cfg.Version)
}

func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		next = append(next, handler.WithAttrs(attrs))
	}
	return &fanoutHandler{handlers: next}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		next = append(next, handler.WithGroup(name))
	}
	return &fanoutHandler{handlers: next}
}

type minLevelHandler struct {
	minLevel slog.Level
	next     slog.Handler
}

func (h *minLevelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.minLevel {
		return false
	}
	return h.next.Enabled(ctx, level)
}

func (h *minLevelHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.next.Handle(ctx, record)
}

func (h *minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &minLevelHandler{minLevel: h.minLevel, next: h.next.WithAttrs(attrs)}
}

func (h *minLevelHandler) WithGroup(name string) slog.Handler {
	return &minLevelHandler{minLevel: h.minLevel, next: h.next.WithGroup(name)}
}

type traceContextHandler struct {
	next slog.Handler
}

func (h *traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceContextHandler) Handle(ctx context.Context, record slog.Record) error {
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, record)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{next: h.next.WithGroup(name)}
}
