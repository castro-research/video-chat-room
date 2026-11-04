package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

const RequestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// RequestID devolve o id da requisição que está no contexto ("" fora de uma requisição).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithRequestID aceita o X-Request-Id do cliente (se for curto e imprimível) ou gera um, devolve-o no header
// da resposta e o põe no contexto, de onde o logger o lê.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// ContextHandler envolve um slog.Handler e acrescenta request_id e trace_id (quando há) aos registros
// feitos com um contexto (slog.InfoContext etc.).
type ContextHandler struct{ slog.Handler }

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ContextHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return ContextHandler{h.Handler.WithAttrs(a)}
}
func (h ContextHandler) WithGroup(n string) slog.Handler {
	return ContextHandler{h.Handler.WithGroup(n)}
}
