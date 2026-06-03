package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"memstation/apps/server/internal/domain"
)

const (
	maxProviderAttempts = 3
	maxProviderBackoff  = 500 * time.Millisecond
)

var (
	providerMetricsOnce     sync.Once
	providerRequestCounter  otelmetric.Int64Counter
	providerRetryCounter    otelmetric.Int64Counter
	providerDurationMetrics otelmetric.Float64Histogram
)

type providerRequestSpec struct {
	Provider domain.Provider
	Method   string
	URL      string
	Headers  map[string]string
	Body     []byte
	Accept   string
	Stream   bool
}

type providerResult struct {
	Body       []byte
	StatusCode int
	Response   *http.Response
}

type ProviderHTTPError struct {
	StatusCode int
	Body       string
}

func (e *ProviderHTTPError) Error() string {
	return fmt.Sprintf("provider request failed: status=%d body=%s", e.StatusCode, sanitizeProviderErrorBody(e.Body))
}

func DoJSONRequest(ctx context.Context, client *http.Client, providerName domain.Provider, method, endpoint string, headers map[string]string, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	result, err := doRequest(ctx, client, providerRequestSpec{
		Provider: providerName,
		Method:   method,
		URL:      endpoint,
		Headers:  headers,
		Body:     body,
		Accept:   "application/json",
	})
	if err != nil {
		return nil, result.StatusCode, err
	}
	return result.Body, result.StatusCode, nil
}

func DoStreamRequest(ctx context.Context, client *http.Client, providerName domain.Provider, method, endpoint string, headers map[string]string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	result, err := doRequest(ctx, client, providerRequestSpec{
		Provider: providerName,
		Method:   method,
		URL:      endpoint,
		Headers:  headers,
		Body:     body,
		Accept:   "text/event-stream",
		Stream:   true,
	})
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

func doRequest(ctx context.Context, client *http.Client, spec providerRequestSpec) (providerResult, error) {
	initProviderMetrics()
	tracer := otel.Tracer("memstation/provider")

	var lastErr error
	var lastStatus int
	for attempt := 1; attempt <= maxProviderAttempts; attempt++ {
		attemptAttrs := []attribute.KeyValue{
			attribute.String("provider.name", providerName(spec.Provider)),
			attribute.String("http.request.method", spec.Method),
			attribute.String("url.scheme", urlScheme(spec.URL)),
			attribute.String("server.address", urlHost(spec.URL)),
			attribute.String("url.path", urlPath(spec.URL)),
			attribute.String("provider.request_kind", requestKind(spec.Stream)),
			attribute.Int("provider.attempt", attempt),
		}
		attemptCtx, span := tracer.Start(ctx, "provider.request", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attemptAttrs...))
		start := time.Now()
		result, err := doSingleRequest(attemptCtx, client, spec)
		durationMS := float64(time.Since(start).Milliseconds())

		if err == nil {
			successAttrs := append(attemptAttrs,
				attribute.Int("http.response.status_code", result.StatusCode),
				attribute.String("provider.result", "success"),
			)
			providerRequestCounter.Add(attemptCtx, 1, otelmetric.WithAttributes(successAttrs...))
			providerDurationMetrics.Record(attemptCtx, durationMS, otelmetric.WithAttributes(successAttrs...))
			span.SetAttributes(
				attribute.Int("http.response.status_code", result.StatusCode),
				attribute.String("provider.result", "success"),
			)
			span.End()
			return result, nil
		}

		lastErr = err
		lastStatus = result.StatusCode
		errorAttrs := append(attemptAttrs,
			attribute.Int("http.response.status_code", result.StatusCode),
			attribute.String("provider.result", "error"),
		)
		providerRequestCounter.Add(attemptCtx, 1, otelmetric.WithAttributes(errorAttrs...))
		providerDurationMetrics.Record(attemptCtx, durationMS, otelmetric.WithAttributes(errorAttrs...))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(
			attribute.Int("http.response.status_code", result.StatusCode),
			attribute.String("provider.result", "error"),
		)
		span.End()

		if attempt == maxProviderAttempts || !shouldRetry(ctx, err) {
			break
		}
		providerRetryCounter.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("provider.name", providerName(spec.Provider)),
			attribute.String("provider.request_kind", requestKind(spec.Stream)),
		))
		if err := waitProviderBackoff(ctx, attempt); err != nil {
			return providerResult{StatusCode: lastStatus}, err
		}
	}
	return providerResult{StatusCode: lastStatus}, lastErr
}

func doSingleRequest(ctx context.Context, client *http.Client, spec providerRequestSpec) (providerResult, error) {
	request, err := http.NewRequestWithContext(ctx, spec.Method, spec.URL, bytes.NewReader(spec.Body))
	if err != nil {
		return providerResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if spec.Accept != "" {
		request.Header.Set("Accept", spec.Accept)
	}
	for key, value := range spec.Headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return providerResult{}, err
	}

	if response.StatusCode >= http.StatusBadRequest {
		raw, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return providerResult{StatusCode: response.StatusCode}, readErr
		}
		return providerResult{StatusCode: response.StatusCode, Body: raw}, &ProviderHTTPError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(raw)),
		}
	}

	if spec.Stream {
		return providerResult{
			StatusCode: response.StatusCode,
			Response:   response,
		}, nil
	}

	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return providerResult{StatusCode: response.StatusCode}, err
	}
	return providerResult{
		StatusCode: response.StatusCode,
		Body:       raw,
	}, nil
}

var providerSecretPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|ms-[a-z0-9_-]{8,}|bearer\s+[a-z0-9._-]{8,}|api[_ -]?key["'\s:=]+[a-z0-9._-]{8,})`)

func sanitizeProviderErrorBody(body string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
	if normalized == "" {
		return ""
	}
	normalized = providerSecretPattern.ReplaceAllString(normalized, "[redacted]")
	const maxErrorBodyChars = 240
	if len(normalized) > maxErrorBodyChars {
		return normalized[:maxErrorBodyChars] + "..."
	}
	return normalized
}

func shouldRetry(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var httpErr *ProviderHTTPError
	if errors.As(err, &httpErr) {
		return shouldRetryStatus(httpErr.StatusCode)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		temporary, ok := err.(interface{ Temporary() bool })
		return ok && temporary.Temporary()
	}
	return true
}

func shouldRetryStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return statusCode >= http.StatusInternalServerError
	}
}

func waitProviderBackoff(ctx context.Context, attempt int) error {
	delay := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
	if delay > maxProviderBackoff {
		delay = maxProviderBackoff
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func initProviderMetrics() {
	providerMetricsOnce.Do(func() {
		meter := otel.Meter("memstation/provider")
		providerRequestCounter, _ = meter.Int64Counter("memstation.provider.requests")
		providerRetryCounter, _ = meter.Int64Counter("memstation.provider.retries")
		providerDurationMetrics, _ = meter.Float64Histogram("memstation.provider.duration_ms")
	})
}

func providerName(value domain.Provider) string {
	if value == "" {
		return "unknown"
	}
	return string(value)
}

func requestKind(stream bool) string {
	if stream {
		return "stream"
	}
	return "json"
}

func urlScheme(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "unknown"
	}
	return parsed.Scheme
}

func urlHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}

func urlPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" {
		return "/"
	}
	return parsed.Path
}
