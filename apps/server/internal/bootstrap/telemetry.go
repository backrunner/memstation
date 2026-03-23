package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellogglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

func SetupTelemetry(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if strings.TrimSpace(cfg.OTLPHTTPEndpoint) == "" {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
		otel.SetMeterProvider(noop.NewMeterProvider())
		otellogglobal.SetLoggerProvider(lognoop.NewLoggerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", cfg.AppName),
			attribute.String("service.version", cfg.Version),
		),
	)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracehttp.New(ctx, traceExporterOptions(cfg.OTLPHTTPEndpoint)...)
	if err != nil {
		return nil, err
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithBatcher(traceExporter),
	)

	metricExporter, err := otlpmetrichttp.New(ctx, metricExporterOptions(cfg.OTLPHTTPEndpoint)...)
	if err != nil {
		_ = traceProvider.Shutdown(ctx)
		return nil, err
	}
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(15*time.Second))),
	)

	logExporter, err := otlploghttp.New(ctx, logExporterOptions(cfg.OTLPHTTPEndpoint)...)
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = traceProvider.Shutdown(ctx)
		return nil, err
	}
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)

	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	otellogglobal.SetLoggerProvider(loggerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return func(ctx context.Context) error {
		return errors.Join(
			loggerProvider.Shutdown(ctx),
			meterProvider.Shutdown(ctx),
			traceProvider.Shutdown(ctx),
		)
	}, nil
}

func traceExporterOptions(endpoint string) []otlptracehttp.Option {
	opts := []otlptracehttp.Option{otlptracehttp.WithTimeout(5 * time.Second)}
	return append(opts, otlpTraceBaseOptions(endpoint)...)
}

func metricExporterOptions(endpoint string) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithTimeout(5 * time.Second)}
	return append(opts, otlpMetricBaseOptions(endpoint)...)
}

func logExporterOptions(endpoint string) []otlploghttp.Option {
	opts := []otlploghttp.Option{otlploghttp.WithTimeout(5 * time.Second)}
	return append(opts, otlpLogBaseOptions(endpoint)...)
}

func otlpTraceBaseOptions(endpoint string) []otlptracehttp.Option {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint)}
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(parsed.Host),
		otlptracehttp.WithURLPath(joinOTLPPath(parsed.Path, "/v1/traces")),
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	return opts
}

func otlpMetricBaseOptions(endpoint string) []otlpmetrichttp.Option {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(endpoint)}
	}

	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(parsed.Host),
		otlpmetrichttp.WithURLPath(joinOTLPPath(parsed.Path, "/v1/metrics")),
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	return opts
}

func otlpLogBaseOptions(endpoint string) []otlploghttp.Option {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return []otlploghttp.Option{otlploghttp.WithEndpointURL(endpoint)}
	}

	opts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(parsed.Host),
		otlploghttp.WithURLPath(joinOTLPPath(parsed.Path, "/v1/logs")),
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	return opts
}

func joinOTLPPath(basePath, suffix string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return suffix
	}
	joined := path.Join("/", basePath, suffix)
	if !strings.HasPrefix(joined, "/") {
		return "/" + joined
	}
	return joined
}
