package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		ServerName:   "Teste",
		Registration: "open",
		JWTSecret:    []byte(strings.Repeat("k", 32)),
	}
	srv := httptest.NewServer(New(cfg, dbtest.New(t)))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestDiscoveryAndHealth(t *testing.T) {
	srv := newTestServer(t)
	status, body := call(t, "GET", srv.URL+"/.well-known/kurz", "", nil)
	if status != 200 || body["name"] != "Teste" || body["registration"] != "open" || body["contract"] != "0.6" {
		t.Fatalf("discovery = %d %v", status, body)
	}
	if caps, _ := body["capabilities"].([]any); len(caps) != 3 || caps[0] != "text" || caps[1] != "realtime" || caps[2] != "search" {
		t.Fatalf("capabilities = %v", body["capabilities"])
	}
	if status, _ := call(t, "GET", srv.URL+"/healthz", "", nil); status != 200 {
		t.Fatalf("healthz = %d", status)
	}
}

func TestAuthFlowEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	creds := map[string]string{"username": "yuri", "password": "password123"}

	status, sess := call(t, "POST", srv.URL+"/v1/auth/register", "", creds)
	if status != 201 {
		t.Fatalf("register = %d %v", status, sess)
	}
	if status, body := call(t, "POST", srv.URL+"/v1/auth/register", "", creds); status != 409 || body["code"] != "username_taken" {
		t.Fatalf("duplicate register = %d %v", status, body)
	}

	// /me exige token
	if status, body := call(t, "GET", srv.URL+"/v1/me", "", nil); status != 401 || body["code"] != "unauthorized" {
		t.Fatalf("me without token = %d %v", status, body)
	}
	access := sess["accessToken"].(string)
	status, me := call(t, "GET", srv.URL+"/v1/me", access, nil)
	if status != 200 || me["username"] != "yuri" || me["status"] != "offline" {
		t.Fatalf("me = %d %v", status, me)
	}

	status, upd := call(t, "PATCH", srv.URL+"/v1/me", access, map[string]any{"name": "Yuri C.", "subtitle": "codando"})
	if status != 200 || upd["name"] != "Yuri C." || upd["subtitle"] != "codando" {
		t.Fatalf("patch me = %d %v", status, upd)
	}

	// login
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/login", "", creds); status != 200 {
		t.Fatalf("login = %d", status)
	}
	if status, body := call(t, "POST", srv.URL+"/v1/auth/login", "", map[string]string{"username": "yuri", "password": "wrongwrong"}); status != 401 {
		t.Fatalf("bad login = %d %v", status, body)
	}

	// refresh: o token antigo para de funcionar. Reapresentado logo em seguida (dentro da janela de graça)
	// é tratado como corrida/retry: 401, mas a família continua viva. O reuso depois da janela, que
	// derruba a família, é coberto em identity (lá o relógio é controlável).
	old := sess["refreshToken"].(string)
	status, next := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": old})
	if status != 200 {
		t.Fatalf("refresh = %d %v", status, next)
	}
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": old}); status != 401 {
		t.Fatalf("reused refresh = %d", status)
	}
	if status, body := call(t, "POST", srv.URL+"/v1/auth/refresh", "", map[string]string{"refreshToken": next["refreshToken"].(string)}); status != 200 {
		t.Fatalf("family must survive a reuse inside the grace window, got %d %v", status, body)
	}

	// logout sempre 204
	if status, _ := call(t, "POST", srv.URL+"/v1/auth/logout", "", map[string]string{"refreshToken": "whatever"}); status != 204 {
		t.Fatalf("logout = %d", status)
	}
}

func TestInvalidBearerTokens(t *testing.T) {
	srv := newTestServer(t)
	for _, tok := range []string{"garbage", "a.b.c"} {
		if status, body := call(t, "GET", srv.URL+"/v1/me", tok, nil); status != 401 || body["code"] != "unauthorized" {
			t.Errorf("token %q: %d %v", tok, status, body)
		}
	}
}
