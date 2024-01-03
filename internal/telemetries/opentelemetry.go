package telemetries

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/zapr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.uber.org/zap"
)

const serviceName = "kong-kic"

func NewExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	// Your preferred exporter: console, jaeger, zipkin, OTLP, etc.
	return otlptracehttp.New(ctx)
}

func NewTraceProvider(exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	// Ensure default SDK resources and the required service name are set.
	r, err := resource.New(
		context.Background(),
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithOSType(),
	)

	if err != nil {
		panic(err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(r),
	)
}

// InitOpenTelemetry initializes OpenTelemetry and returns a trace.Tracer
func InitOpenTelemetry() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	zaplog, err := zap.NewDevelopment()
	if err != nil {
		os.Exit(1)
	}
	log := zapr.NewLogger(zaplog)
	log.Info("Initializing OpenTelemetry...")
	log.Info("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")

	// Create OTLP exporter to export traces to the OpenTelemetry Collector
	exporter, err := NewExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create the collector exporter: %w", err)
	}

	// Create a new trace provider with a batch span processor and the OTLP exporter
	tp := NewTraceProvider(exporter)
	// Handle shutdown properly so nothing leaks.
	// defer func() {
	// 	_ = tp.Shutdown(ctx)
	// 	log.Error(nil, "OpenTelemetry shutdown")
	// }()

	// Set the global trace provider
	otel.SetTracerProvider(tp)

	return tp, nil
}
