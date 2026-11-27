package obs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TracingOptions is the tracing options block. Embed it in an options
// object under the key "tracing"; its flag is --tracing.endpoint.
type TracingOptions struct {
	// Endpoint is the OTLP/HTTP traces URL of the backend, such as
	// http://otel-collector:4318/v1/traces; empty exports nothing.
	Endpoint string `mapstructure:"endpoint"`
}

// NewTracingOptions returns the default tracing options: no backend.
func NewTracingOptions() *TracingOptions { return &TracingOptions{} }

// AddFlags declares the block's flags on fs.
func (o *TracingOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Endpoint, "tracing.endpoint", o.Endpoint,
		"OTLP/HTTP traces URL to export spans to, such as http://otel-collector:4318/v1/traces; empty exports nothing")
}

// Complete does nothing; the block has no derived fields.
func (o *TracingOptions) Complete() error { return nil }

// Validate reports an endpoint that is not an http or https URL with a host.
func (o *TracingOptions) Validate() error {
	if o.Endpoint == "" {
		return nil
	}
	u, err := url.Parse(o.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("tracing.endpoint: invalid value %q, want an http or https URL", o.Endpoint)
	}
	return nil
}

// Tracer starts the spans of a process. It always generates trace IDs;
// with a backend configured it also exports the spans with OTLP/HTTP in
// batches. Create one per process and pass it to every server.
//
// It is also a lifecycle component whose Stop exports the spans not
// exported yet: run it first, so it stops last, once every server has
// drained, within the same shutdown timeout.
type Tracer struct {
	tp         *sdktrace.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TraceContext
}

// NewTracer returns a tracer configured by opts whose spans name service as
// their service. It does not contact the backend.
func NewTracer(ctx context.Context, opts *TracingOptions, service string) (*Tracer, error) {
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
	}
	if opts.Endpoint != "" {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(opts.Endpoint))
		if err != nil {
			return nil, fmt.Errorf("tracing exporter: %w", err)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	return &Tracer{tp: tp, tracer: tp.Tracer("github.com/hongling2511/corekit")}, nil
}

// StartServerSpan starts a server span named name for a request with
// header. It continues the upstream trace of a W3C traceparent header and
// starts a new trace otherwise. It returns ctx carrying the span, the trace
// ID and the function that ends the span.
func (t *Tracer) StartServerSpan(ctx context.Context, name string, header http.Header) (context.Context, string, func()) {
	ctx = t.propagator.Extract(ctx, propagation.HeaderCarrier(header))
	ctx, span := t.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
	return ctx, span.SpanContext().TraceID().String(), func() { span.End() }
}

// Name returns "tracing".
func (t *Tracer) Name() string { return "tracing" }

// Start does nothing until ctx is cancelled; spans are started by the
// servers and exported in the background.
func (t *Tracer) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Stop exports the spans not exported yet and stops the tracer, giving up
// when ctx is done.
func (t *Tracer) Stop(ctx context.Context) error {
	if err := t.tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("tracing shutdown: %w", err)
	}
	return nil
}
