package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
)

// newOpsServer sobe a app com workers e a config ajustada (rate limit ligado, proxy confiável...).
func newOpsServer(t *testing.T, tweak func(*config.Config)) *httptest.Server {
	t.Helper()
	cfg := config.Config{ServerName: "Teste", Registration: "open", JWTSecret: []byte(strings.Repeat("k", 32))}
	tweak(&cfg)
	app := NewApp(cfg, dbtest.New(t))
	srv := httptest.NewServer(app.Handler)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); app.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait(); srv.Close() })
	return srv
}

func rawDo(t *testing.T, method, url string, hdr map[string]string, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestRateLimitAuthByIPAndProxyHeader(t *testing.T) {
	srv := newOpsServer(t, func(c *config.Config) {
		c.RateLimit = config.RateLimit{Enabled: true, AuthBurst: 3, AuthPerMin: 1, WriteBurst: 100, WritePerSec: 100}
	})
	login := func(hdr map[string]string) *http.Response {
		return rawDo(t, "POST", srv.URL+"/v1/auth/login", hdr, `{"username":"ninguem","password":"password123"}`)
	}
	for i := 0; i < 3; i++ {
		if res := login(nil); res.StatusCode != 401 {
			t.Fatalf("login %d = %d, want 401 (credencial errada, mas dentro do limite)", i, res.StatusCode)
		}
	}
	res := login(nil)
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 429 || !strings.Contains(string(b), `"rate_limited"`) {
		t.Fatalf("over limit = %d %s", res.StatusCode, b)
	}
	if ra := res.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After = %q", ra)
	}
	// sem KURZ_TRUSTED_PROXY, o X-Forwarded-For é ignorado: forjar IP não escapa do limite
	if res := login(map[string]string{"X-Forwarded-For": "9.9.9.9"}); res.StatusCode != 429 {
		t.Fatalf("spoofed XFF without trusted proxy = %d", res.StatusCode)
	}
	// /metrics e /healthz não entram no limite
	for i := 0; i < 10; i++ {
		if res := rawDo(t, "GET", srv.URL+"/metrics", nil, ""); res.StatusCode != 200 {
			t.Fatalf("metrics = %d", res.StatusCode)
		}
	}
}

func TestRateLimitTrustedProxyUsesForwardedIP(t *testing.T) {
	srv := newOpsServer(t, func(c *config.Config) {
		c.TrustedProxy = true
		c.RateLimit = config.RateLimit{Enabled: true, AuthBurst: 1, AuthPerMin: 1, WriteBurst: 100, WritePerSec: 100}
	})
	login := func(ip string) int {
		return rawDo(t, "POST", srv.URL+"/v1/auth/login", map[string]string{"X-Forwarded-For": "6.6.6.6, " + ip}, `{"username":"x","password":"password123"}`).StatusCode
	}
	if login("1.1.1.1") != 401 || login("1.1.1.1") != 429 {
		t.Fatal("same client IP must share a bucket")
	}
	if login("2.2.2.2") != 401 {
		t.Fatal("another client IP has its own bucket")
	}
}

func TestRateLimitWritesPerUser(t *testing.T) {
	srv := newOpsServer(t, func(c *config.Config) {
		c.RateLimit = config.RateLimit{Enabled: true, AuthBurst: 100, AuthPerMin: 100, WriteBurst: 3, WritePerSec: 0.01}
	})
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	gid := createGuild(alice, "Casa") // escrita 1
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "1"}) // 2
	alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "2"}) // 3
	alice.fail("POST", "/v1/channels/"+ch+"/messages", 429, "rate_limited", map[string]any{"text": "3"})
	alice.do("GET", "/v1/channels/"+ch+"/messages", 200, nil)            // leitura livre
	bob.do("PATCH", "/v1/me", 200, map[string]any{"displayName": "Bob"}) // outro usuário, outro bucket
}

// O limitador lê o Authorization com a mesma função do Require: "bearer <jwt>" em minúsculas também cai no bucket do usuário
// (e não no do IP, que clientes atrás do mesmo NAT dividiriam).
func TestRateLimitUserBucketAcceptsLowercaseBearer(t *testing.T) {
	srv := newOpsServer(t, func(c *config.Config) {
		c.RateLimit = config.RateLimit{Enabled: true, AuthBurst: 100, AuthPerMin: 100, WriteBurst: 2, WritePerSec: 0.01}
	})
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	patch := func(u *user) int {
		return rawDo(t, "PATCH", srv.URL+"/v1/me", map[string]string{"Authorization": "bearer " + u.token}, `{"subtitle":"x"}`).StatusCode
	}
	if patch(alice) != 200 || patch(alice) != 200 || patch(alice) != 429 {
		t.Fatal("alice must be limited by her own bucket")
	}
	if got := patch(bob); got != 200 {
		t.Fatalf("bob shares alice's bucket (ip:) = %d", got)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newOpsServer(t, func(c *config.Config) {})
	alice := newUser(t, srv.URL, "alice")
	gid := createGuild(alice, "Casa")
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	cb := connect(t, srv, alice)
	alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "oi"})
	cb.event("message.create")
	rawDo(t, "GET", srv.URL+"/nao-existe", nil, "")
	res := rawDo(t, "GET", srv.URL+"/v1/channels/"+ch+"/messages", map[string]string{"Authorization": "Bearer " + alice.token}, "")
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id")
	}
	if res := rawDo(t, "GET", srv.URL+"/healthz", map[string]string{"X-Request-Id": "abc-123"}, ""); res.Header.Get("X-Request-Id") != "abc-123" {
		t.Fatalf("request id not echoed: %q", res.Header.Get("X-Request-Id"))
	}

	// leva um instante até a conexão contar e o outbox esvaziar; as séries precisam existir de qualquer forma
	var text string
	deadline := time.Now().Add(5 * time.Second)
	for {
		res := rawDo(t, "GET", srv.URL+"/metrics", nil, "")
		b, _ := io.ReadAll(res.Body)
		text = string(b)
		if strings.Contains(text, "kurz_ws_connections 1") && strings.Contains(text, "kurz_gateway_events_delivered_total") && !strings.Contains(text, "kurz_gateway_events_delivered_total 0\n") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{
		`kurz_http_requests_total{method="POST",route="POST /v1/channels/{channelId}/messages",status="201"} 1`,
		`kurz_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`kurz_http_request_duration_seconds_bucket{method="GET",route="GET /v1/channels/{channelId}/messages",le="0.1"}`,
		`kurz_ws_connections 1`,
		`kurz_gateway_events_dropped_total 0`,
		`kurz_gateway_slow_disconnects_total 0`,
		`kurz_outbox_lag_seconds`,
		`kurz_outbox_pending`,
		`kurz_jobs_pending`,
		`kurz_jobs_failed`,
		`kurz_pgx_pool_connections{state="total"}`,
		`kurz_pgx_pool_max_connections`,
		`go_goroutines`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics sem %q", want)
		}
	}
	if strings.Contains(text, "kurz_gateway_events_delivered_total 0\n") {
		t.Error("events delivered should be > 0")
	}
	if strings.Contains(text, "/v1/channels/1") { // sem ids na cardinalidade
		t.Error("route label leaks ids")
	}
}
