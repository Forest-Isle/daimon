package observability

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// InitTracer initializes the global tracer provider and propagators.
func InitTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(newPropagator())

	if !cfg.Enabled || cfg.Exporter == "" || cfg.Exporter == "noop" {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		return noopShutdown, nil
	}

	cfg = cfg.normalized()

	res, err := newResource(cfg.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	exporter, err := newSpanExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(newSampler(cfg.SampleRate)),
	)

	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns a global tracer from the configured provider.
func Tracer(name string) trace.Tracer {
	if name == "" {
		name = defaultLibraryName
	}
	return otel.Tracer(name)
}

// StartSpan starts a span from the package default tracer.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer("").Start(ctx, name, opts...)
}

func newSpanExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "otlp_grpc":
		opts := make([]otlptracegrpc.Option, 0, 2)
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint), otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)
	case "otlp_http":
		opts := make([]otlptracehttp.Option, 0, 2)
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(cfg.Endpoint), otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	case "stdout":
		return stdouttrace.New(
			stdouttrace.WithWriter(os.Stdout),
			stdouttrace.WithPrettyPrint(),
		)
	default:
		return nil, fmt.Errorf("unsupported otel trace exporter %q", cfg.Exporter)
	}
}

func newSampler(rate float64) sdktrace.Sampler {
	if rate >= 1.0 {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newResource(serviceName string) (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion()),
		),
	)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func serviceVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func noopShutdown(context.Context) error {
	return nil
}
