package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupDisabledIsNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), "", "kurz", "0")
	if err != nil || shutdown(context.Background()) != nil {
		t.Fatal("empty endpoint must be a no-op")
	}
}

func TestSpansReachOTLPCollector(t *testing.T) {
	var posts atomic.Int32
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/traces" {
			posts.Add(1)
		}
		w.WriteHeader(200)
	}))
	defer col.Close()
	shutdown, err := Setup(context.Background(), col.URL, "kurz-test", "0")
	if err != nil {
		t.Fatal(err)
	}
	var sawTrace bool
	h := HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTrace = trace.SpanContextFromContext(r.Context()).IsValid()
		_, span := otel.Tracer("test").Start(r.Context(), "child")
		span.End()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/me", nil))
	if !sawTrace {
		t.Fatal("handler should run inside a span")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil { // flush
		t.Fatal(err)
	}
	if posts.Load() == 0 {
		t.Fatal("collector received no OTLP export")
	}
}
