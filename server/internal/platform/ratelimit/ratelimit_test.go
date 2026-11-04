package ratelimit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestBucketBurstAndRefill(t *testing.T) {
	c := &clock{time.Unix(1000, 0)}
	l := New(2, 3, c.now) // 2 tokens/s, rajada 3
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d should pass within the burst", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != 500*time.Millisecond {
		t.Fatalf("4th: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok { // chaves independentes
		t.Fatal("other key must have its own bucket")
	}
	c.advance(500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("one token should have been refilled")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token was refilled")
	}
	c.advance(time.Hour) // nunca passa do burst
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("full bucket")
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("burst is a hard cap")
	}
}

func TestSweepRemovesOnlyIdleBuckets(t *testing.T) {
	c := &clock{time.Unix(1000, 0)}
	l := New(1, 2, c.now)
	l.Allow("idle")
	l.Allow("busy")
	l.Allow("busy")
	l.Allow("busy") // esvazia
	if n := l.Sweep(); n != 0 {
		t.Fatalf("nothing is full yet, swept %d", n)
	}
	c.advance(1 * time.Second) // idle volta a cheio (2); busy tem 1
	if n := l.Sweep(); n != 1 || l.Len() != 1 {
		t.Fatalf("swept=%d len=%d", n, l.Len())
	}
	c.advance(5 * time.Second)
	if n := l.Sweep(); n != 1 || l.Len() != 0 {
		t.Fatalf("swept=%d len=%d", n, l.Len())
	}
	// descartar o bucket não muda o comportamento: a chave volta cheia
	if ok, _ := l.Allow("busy"); !ok {
		t.Fatal("fresh key starts full")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.9:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	if ip := ClientIP(r, false); ip != "10.0.0.9" {
		t.Fatalf("untrusted must ignore XFF, got %s", ip)
	}
	if ip := ClientIP(r, true); ip != "203.0.113.7" {
		t.Fatalf("trusted must use the proxy's entry, got %s", ip)
	}
	r.Header.Del("X-Forwarded-For")
	if ip := ClientIP(r, true); ip != "10.0.0.9" {
		t.Fatalf("no XFF falls back to the peer, got %s", ip)
	}
}

func TestMiddleware(t *testing.T) {
	c := &clock{time.Unix(1000, 0)}
	m := &Middleware{
		Auth:  New(1, 2, c.now),
		Write: New(1, 3, c.now),
		User: func(r *http.Request) (int64, bool) {
			if h := r.Header.Get("X-User"); h != "" {
				return int64(len(h)), true // usuários distintos por tamanho do header
			}
			return 0, false
		},
	}
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(method, path, ip, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(""))
		r.RemoteAddr = ip + ":1"
		if user != "" {
			r.Header.Set("X-User", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// auth: por IP, rajada 2
	for i := 0; i < 2; i++ {
		if w := do("POST", "/v1/auth/login", "1.1.1.1", ""); w.Code != 204 {
			t.Fatalf("auth %d = %d", i, w.Code)
		}
	}
	w := do("POST", "/v1/auth/login", "1.1.1.1", "")
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("auth over limit = %d retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "rate_limited" {
		t.Fatalf("body = %v", body)
	}
	if w := do("POST", "/v1/auth/login", "2.2.2.2", ""); w.Code != 204 {
		t.Fatal("other IP has its own bucket")
	}
	// escrita: por usuário, rajada 3; leituras e outro usuário não contam
	for i := 0; i < 3; i++ {
		do("POST", "/v1/channels/1/messages", "1.1.1.1", "u")
	}
	if w := do("POST", "/v1/channels/1/messages", "1.1.1.1", "u"); w.Code != 429 {
		t.Fatalf("write over limit = %d", w.Code)
	}
	if w := do("POST", "/v1/channels/1/messages", "1.1.1.1", "uu"); w.Code != 204 {
		t.Fatal("other user has its own bucket (same IP)")
	}
	for i := 0; i < 20; i++ {
		for _, p := range []string{"/v1/me", "/metrics", "/healthz"} {
			if w := do("GET", p, "1.1.1.1", "u"); w.Code != 204 {
				t.Fatalf("GET %s must not be limited", p)
			}
		}
	}
	// escrita sem token válido cai no limite por IP
	for i := 0; i < 3; i++ {
		do("DELETE", "/v1/x", "3.3.3.3", "")
	}
	if w := do("DELETE", "/v1/x", "3.3.3.3", ""); w.Code != 429 {
		t.Fatal("anonymous write limited by IP")
	}
	c.advance(time.Second)
	if w := do("POST", "/v1/auth/login", "1.1.1.1", ""); w.Code != 204 {
		t.Fatal("refilled")
	}
}
