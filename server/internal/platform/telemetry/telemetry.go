// Package telemetry liga os traces OpenTelemetry (RNF14). É opcional: sem KURZ_OTEL_ENDPOINT nada é instalado
// e o custo é zero.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// Setup instala o TracerProvider global exportando por OTLP/HTTP para endpoint (URL, ex.: http://otel:4318).
// Com endpoint vazio devolve um shutdown que não faz nada.
func Setup(ctx context.Context, endpoint, service, instance string) (shutdown func(context.Context) error, err error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("KURZ_OTEL_ENDPOINT must be an http(s) URL, e.g. http://otel:4318")
	}
	if u.Path == "" || u.Path == "/" { // WithEndpointURL usaria "/" em vez do caminho padrão do OTLP
		u.Path = "/v1/traces"
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u.String()))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res := resource.NewSchemaless(semconv.ServiceName(service), attribute.String("service.instance.id", instance))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}

// HTTP envolve o handler com um span por requisição. /metrics e /healthz ficam de fora (ruído).
func HTTP(h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, "kurz", otelhttp.WithFilter(func(r *http.Request) bool {
		return r.URL.Path != "/metrics" && r.URL.Path != "/healthz"
	}), otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method // o ServeMux ainda não casou a rota aqui; http.route vai como atributo ao fim
	}))
}

// PGX instala o tracer do pgx (um span por query) na config do pool.
func PGX(cfg *pgxpool.Config) {
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())
}
