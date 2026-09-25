// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// SetupTelemetry configures OTLP (HTTP) trace and metric export when enabled.
// Endpoints and headers come from the standard OTEL_EXPORTER_OTLP_* variables.
// When disabled, the global providers stay no-op. The returned function
// flushes and shuts the providers down.
func SetupTelemetry(ctx context.Context, enabled bool, service, version string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	if !enabled {
		return func(context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(service),
		semconv.ServiceVersion(version),
		semconv.ServiceNamespace("dbr2"),
	))
	if err != nil {
		return nil, err
	}
	te, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(te), sdktrace.WithResource(res))
	me, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me)), sdkmetric.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

func spanIDs(ctx context.Context) (string, string, bool) {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", "", false
	}
	return sc.TraceID().String(), sc.SpanID().String(), true
}

// TraceID returns the current trace ID, or "".
func TraceID(ctx context.Context) string {
	tid, _, _ := spanIDs(ctx)
	return tid
}
