// Package observability provides OpenTelemetry distributed tracing for DFMS.
//
// Tracing is configured per-service and exports spans via OTLP gRPC to
// an OpenTelemetry Collector, which forwards them to Grafana Tempo.
//
// Sampling strategy:
//   - Development: 100% (AlwaysSample) — see every trace for debugging
//   - Production: 10% (TraceIDRatioBased) — control cost/volume
package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerShutdownFunc is a function that shuts down the tracer provider,
// flushing any remaining spans. Must be called on service shutdown.
type TracerShutdownFunc func(context.Context) error

// InitTracer initializes OpenTelemetry tracing for a service.
// It sets up:
//   - OTLP gRPC exporter targeting the OTel Collector
//   - Service resource attributes (name, version, environment)
//   - Sampling based on mode (100% dev, 10% prod)
//   - Global tracer + propagator registration
//
// Returns a shutdown function that must be deferred by the caller.
func InitTracer(ctx context.Context, serviceName, mode, collectorEndpoint string) (TracerShutdownFunc, error) {
	if collectorEndpoint == "" {
		collectorEndpoint = "localhost:4317"
	}

	// OTLP gRPC exporter → OTel Collector
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(collectorEndpoint),
		otlptracegrpc.WithInsecure(), // No TLS in dev; prod should use TLS
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Resource describes the service
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(AppVersion),
			semconv.DeploymentEnvironment(mode),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Sampler: capture everything in dev, 10% in production
	var sampler sdktrace.Sampler
	if mode == "production" {
		sampler = sdktrace.TraceIDRatioBased(0.1)
	} else {
		sampler = sdktrace.AlwaysSample()
	}

	// Tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Register globally so otelgin/otelgrpc middleware can find it
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C standard
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer for creating manual spans.
// Usage: span := observability.Tracer("chunking").Start(ctx, "CDC.Split")
func Tracer(name string) trace.Tracer {
	return otel.Tracer(fmt.Sprintf("github.com/AnirudhSinghRajora/DFMS/%s", name))
}
