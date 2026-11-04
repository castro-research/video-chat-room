package httpx

import (
	"log/slog"
	"net/http"
	"time"
)

// Recover captura pânico, loga e responde 500.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.ErrorContext(r.Context(), "panic", "value", v, "method", r.Method, "path", r.URL.Path)
				WriteError(w, errInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

// Status devolve o status escrito (200 se ninguém chamou WriteHeader).
func (s *statusWriter) Status() int { return s.status }

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap deixa http.ResponseController (Flush, Hijack do WebSocket, deadlines) chegar ao writer original.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog loga método, rota, caminho, status e duração de cada requisição (com request_id via ContextHandler).
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.InfoContext(r.Context(), "request", "method", r.Method, "route", r.Pattern, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}
