package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"kurz/server/internal/platform/httpx"
)

// requireProbe monta Require na frente de um handler que devolve o usuário do contexto.
func requireProbe(tokens *TokenIssuer, authorization string) (int, map[string]string) {
	h := httpx.Wrap(Require(tokens)(func(w http.ResponseWriter, r *http.Request) error {
		id, _ := httpx.UserID(r.Context())
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10)})
		return nil
	}))
	req := httptest.NewRequest("GET", "/", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestRequireAcceptsBearerSchemeCaseInsensitively(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	tok, err := iss.Issue(7)
	if err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "bEaReR"} {
		status, body := requireProbe(iss, scheme+" "+tok)
		if status != 200 || body["id"] != "7" {
			t.Errorf("%s: %d %v", scheme, status, body)
		}
	}
}

func TestRequireRejectsMissingOrEmptyToken(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	tok, _ := iss.Issue(7)
	for _, h := range []string{"", "Bearer", "Bearer ", "bearer   ", "Basic " + tok, "Bearer" + tok, "garbage"} {
		if status, body := requireProbe(iss, h); status != 401 || body["code"] != "unauthorized" {
			t.Errorf("%q: %d %v", h, status, body)
		}
	}
}

func TestRequireReportsExpiredToken(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	iss.now = func() time.Time { return time.Now().Add(-time.Hour) } // emitido há 1h, TTL de 1 min
	tok, err := iss.Issue(7)
	if err != nil {
		t.Fatal(err)
	}
	iss.now = time.Now
	for _, scheme := range []string{"Bearer", "bearer"} {
		if status, body := requireProbe(iss, scheme+" "+tok); status != 401 || body["code"] != "token_expired" {
			t.Errorf("%s: %d %v", scheme, status, body)
		}
	}
}
