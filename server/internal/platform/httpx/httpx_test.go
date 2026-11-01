package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func body(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	return m
}

func TestWriteErrorUsesTypedError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, NewError(409, "username_taken", "taken"))
	if rec.Code != 409 {
		t.Fatalf("status = %d", rec.Code)
	}
	if m := body(t, rec); m["code"] != "username_taken" || m["message"] != "taken" {
		t.Fatalf("body = %v", m)
	}
}

func TestWriteErrorHidesUnknownErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errors.New("password=hunter2 leaked"))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("internal error message leaked to the client")
	}
	if body(t, rec)["code"] != "internal" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDecodeRejectsBadJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("{nope"))
	var v struct{ A string }
	err := Decode(r, &v)
	var he *Error
	if !errors.As(err, &he) || he.Status != 422 || he.Code != "validation" {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeOK(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"x"}`))
	var v struct{ A string }
	if err := Decode(r, &v); err != nil || v.A != "x" {
		t.Fatalf("v = %+v, err = %v", v, err)
	}
}

func TestWrapTurnsReturnedErrorIntoResponse(t *testing.T) {
	h := Wrap(func(w http.ResponseWriter, r *http.Request) error { return ErrForbidden })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 403 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	h := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || body(t, rec)["code"] != "internal" {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserIDContext(t *testing.T) {
	if _, ok := UserID(context.Background()); ok {
		t.Fatal("empty context must not have a user")
	}
	ctx := WithUserID(context.Background(), 42)
	if id, ok := UserID(ctx); !ok || id != 42 {
		t.Fatalf("id = %d, ok = %v", id, ok)
	}
}
