// Package ratelimit implementa o rate limit do RNF10: token bucket em memória por chave (usuário ou IP).
//
// Os buckets vivem no processo: com N réplicas o limite efetivo de um cliente é de até N vezes o configurado
// (dívida conhecida; um limite global exigiria Redis ou afinidade de sessão no balanceador).
package ratelimit

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kurz/server/internal/platform/httpx"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter é um conjunto de token buckets: cada chave começa cheia (burst) e repõe rate tokens por segundo.
type Limiter struct {
	rate  float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New cria um Limiter. now pode ser nil (relógio real); testes injetam um relógio falso.
func New(ratePerSec, burst float64, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{rate: ratePerSec, burst: burst, now: now, buckets: map[string]*bucket{}}
}

// Allow gasta um token de key. Se não há, devolve false e quanto falta esperar por um token.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	t := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: t}
		l.buckets[key] = b
	} else {
		b.tokens = math.Min(l.burst, b.tokens+t.Sub(b.last).Seconds()*l.rate)
		b.last = t
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// Sweep apaga os buckets ociosos: os que já reporiam até ficar cheios. Apagá-los não muda nenhuma decisão,
// porque uma chave nova também começa cheia. Devolve quantos apagou.
func (l *Limiter) Sweep() int {
	t := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for k, b := range l.buckets {
		if b.tokens+t.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
			n++
		}
	}
	return n
}

// Len é o número de buckets vivos (para testes e métricas).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Run varre os buckets ociosos a cada interval até ctx terminar.
func (l *Limiter) Run(ctx context.Context, interval time.Duration) {
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			l.Sweep()
		}
	}
}

// ClientIP devolve o IP do cliente. O X-Forwarded-For só vale com trustProxy: então usa a entrada mais à direita,
// a que o NOSSO proxy acrescentou (as da esquerda o cliente pode forjar).
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Middleware aplica os limites: auth (POST /v1/auth/*) por IP e escritas (POST/PUT/PATCH/DELETE) por usuário.
// Um limiter nil não limita aquela classe. /metrics, /healthz e leituras passam direto.
type Middleware struct {
	Auth       *Limiter
	Write      *Limiter
	TrustProxy bool
	// User diz quem é o autor da requisição pelo token, sem exigir autenticação válida. Sem usuário, a escrita é limitada por IP.
	User func(*http.Request) (int64, bool)
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var lim *Limiter
		var key string
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if strings.HasPrefix(r.URL.Path, "/v1/auth/") {
				lim, key = m.Auth, "ip:"+ClientIP(r, m.TrustProxy)
			} else {
				lim = m.Write
				if uid, ok := m.userOf(r); ok {
					key = "user:" + strconv.FormatInt(uid, 10)
				} else {
					key = "ip:" + ClientIP(r, m.TrustProxy)
				}
			}
		}
		if lim != nil {
			if ok, wait := lim.Allow(key); !ok {
				secs := int(math.Ceil(wait.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
				httpx.WriteError(w, httpx.NewError(http.StatusTooManyRequests, "rate_limited", "too many requests"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) userOf(r *http.Request) (int64, bool) {
	if m.User == nil {
		return 0, false
	}
	return m.User(r)
}
