// Package obs expõe as métricas Prometheus (RNF14). Cada App tem o seu registro, então testes não colidem.
//
// /metrics é interno: sem auth e fora do rate limit. Com KURZ_METRICS_ADDR ele sai do listener da API e vai para um
// listener próprio (no Kubernetes, uma porta que nenhum Service público expõe; o Prometheus raspa pelo IP do pod).
// Sem KURZ_METRICS_ADDR (dev) continua no listener principal: não o exponha na Internet.
package obs

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GatewayStats é o que o coletor lê do gateway.
type GatewayStats struct {
	Connections int
	Sessions    int
	Delivered   int64 // eventos enfileirados para sessões
	Dropped     int64 // eventos que estouraram a fila do cliente (backpressure)
	SlowKills   int64 // conexões derrubadas por cliente lento
}

type Metrics struct {
	Registry *prometheus.Registry
	duration *prometheus.HistogramVec
	requests *prometheus.CounterVec
}

// New cria o registro com as métricas de HTTP e os coletores de runtime, gateway, banco e pool.
// gw e pool podem ser nil (testes).
func New(pool *pgxpool.Pool, gw func() GatewayStats) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "kurz_http_request_duration_seconds", Help: "Latência das requisições HTTP por rota (padrão do ServeMux, sem ids).",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kurz_http_requests_total", Help: "Requisições HTTP por rota e status.",
		}, []string{"method", "route", "status"}),
	}
	reg.MustRegister(m.duration, m.requests, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	if gw != nil {
		reg.MustRegister(
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "kurz_ws_connections", Help: "Conexões WebSocket ativas neste processo."},
				func() float64 { return float64(gw().Connections) }),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "kurz_ws_sessions", Help: "Sessões do gateway (inclui as aguardando resume)."},
				func() float64 { return float64(gw().Sessions) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "kurz_gateway_events_delivered_total", Help: "Eventos enfileirados para clientes."},
				func() float64 { return float64(gw().Delivered) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "kurz_gateway_events_dropped_total", Help: "Eventos perdidos por backpressure (fila do cliente cheia)."},
				func() float64 { return float64(gw().Dropped) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "kurz_gateway_slow_disconnects_total", Help: "Conexões derrubadas por cliente lento."},
				func() float64 { return float64(gw().SlowKills) }),
		)
	}
	if pool != nil {
		reg.MustRegister(&dbCollector{pool: pool}, &poolCollector{pool: pool})
	}
	return m
}

// Handler serve /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// Method limita a cardinalidade do label `method`: o cliente escolhe o método HTTP, então só os padrão viram série própria
// e o resto agrupa em "OTHER".
func Method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return m
	}
	return "OTHER"
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int)        { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Middleware mede cada requisição. A rota é o padrão que o ServeMux casou (r.Pattern, preenchido no mesmo *Request
// que passamos adiante), então a cardinalidade é a do número de rotas; sem casamento vira "unmatched".
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		method := Method(r.Method)
		m.requests.WithLabelValues(method, route, strconv.Itoa(rec.status)).Inc()
		if route != "GET /v1/gateway" { // o WebSocket vive minutos: mediria a conexão, não uma requisição
			m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		}
	})
}

// dbCollector consulta, a cada raspagem, o que só o banco sabe: lag do outbox e fila de jobs.
type dbCollector struct{ pool *pgxpool.Pool }

var (
	outboxLagDesc = prometheus.NewDesc("kurz_outbox_lag_seconds", "Idade do evento pendente mais antigo do outbox (0 se não há).", nil, nil)
	outboxPending = prometheus.NewDesc("kurz_outbox_pending", "Eventos do outbox ainda não publicados.", nil, nil)
	jobsPending   = prometheus.NewDesc("kurz_jobs_pending", "Jobs na fila (inclui os agendados para o futuro).", nil, nil)
	jobsFailed    = prometheus.NewDesc("kurz_jobs_failed", "Jobs que esgotaram as tentativas.", nil, nil)
)

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- outboxLagDesc
	ch <- outboxPending
	ch <- jobsPending
	ch <- jobsFailed
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var lag float64
	var pending, jp, jf int64
	err := c.pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8, count(*) FROM outbox WHERE published_at IS NULL`).Scan(&lag, &pending)
	if err == nil {
		ch <- prometheus.MustNewConstMetric(outboxLagDesc, prometheus.GaugeValue, lag)
		ch <- prometheus.MustNewConstMetric(outboxPending, prometheus.GaugeValue, float64(pending))
	}
	err = c.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE done_at IS NULL AND failed_at IS NULL), count(*) FILTER (WHERE failed_at IS NOT NULL) FROM jobs WHERE done_at IS NULL`).Scan(&jp, &jf)
	if err == nil {
		ch <- prometheus.MustNewConstMetric(jobsPending, prometheus.GaugeValue, float64(jp))
		ch <- prometheus.MustNewConstMetric(jobsFailed, prometheus.GaugeValue, float64(jf))
	}
}

// poolCollector expõe pgxpool.Stat.
type poolCollector struct{ pool *pgxpool.Pool }

var (
	poolConns    = prometheus.NewDesc("kurz_pgx_pool_connections", "Conexões do pool por estado.", []string{"state"}, nil)
	poolMax      = prometheus.NewDesc("kurz_pgx_pool_max_connections", "Tamanho máximo do pool.", nil, nil)
	poolAcquires = prometheus.NewDesc("kurz_pgx_pool_acquires_total", "Aquisições de conexão.", nil, nil)
	poolEmpty    = prometheus.NewDesc("kurz_pgx_pool_empty_acquires_total", "Aquisições que esperaram porque o pool estava vazio.", nil, nil)
	poolWait     = prometheus.NewDesc("kurz_pgx_pool_acquire_wait_seconds_total", "Tempo total esperando por conexão.", nil, nil)
)

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolConns, poolMax, poolAcquires, poolEmpty, poolWait} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(s.AcquiredConns()), "acquired")
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(s.IdleConns()), "idle")
	ch <- prometheus.MustNewConstMetric(poolConns, prometheus.GaugeValue, float64(s.TotalConns()), "total")
	ch <- prometheus.MustNewConstMetric(poolMax, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(poolAcquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolEmpty, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolWait, prometheus.CounterValue, s.AcquireDuration().Seconds())
}
