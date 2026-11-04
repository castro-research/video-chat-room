package objstore

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func httpDo(t *testing.T, method, url string, body []byte) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

// contract roda o mesmo roteiro contra qualquer Store cujas URLs sejam alcançáveis.
func contract(t *testing.T, s Store) {
	ctx := context.Background()
	key := "t/" + hex.EncodeToString([]byte(t.Name()))[:8] + "/obj"
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat of missing = %v", err)
	}
	put, err := s.PresignPut(ctx, key, 5, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := httpDo(t, http.MethodPut, put, []byte("hello world")); code != 403 { // o Content-Length assinado é 5
		t.Fatalf("put larger than signed = %d, want 403", code)
	}
	if code, _, _ := httpDo(t, http.MethodPut, put, []byte("hi")); code != 403 {
		t.Fatalf("put smaller than signed = %d, want 403", code)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a rejected put left an object: %v", err)
	}
	if code, _, _ := httpDo(t, http.MethodPut, put, []byte("hello")); code != 200 {
		t.Fatalf("put = %d", code)
	}
	info, err := s.Stat(ctx, key)
	if err != nil || info.Size != 5 {
		t.Fatalf("stat = %+v, %v", info, err)
	}
	get, err := s.PresignGet(ctx, key, GetOptions{Filename: "a b.txt", ContentType: "text/plain", Inline: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	code, body, h := httpDo(t, http.MethodGet, get, nil)
	if code != 200 || string(body) != "hello" || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
		t.Fatalf("get = %d %q %v", code, body, h)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil { // idempotente
		t.Fatalf("second delete = %v", err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat after delete = %v", err)
	}
}

func TestFakeContract(t *testing.T) {
	f := NewFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	f.SetBaseURL(srv.URL)
	contract(t, f)
}

func TestFakeExpiryAndMethod(t *testing.T) {
	f := NewFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	f.SetBaseURL(srv.URL)
	now := time.Now()
	f.SetClock(func() time.Time { return now })
	put, _ := f.PresignPut(context.Background(), "k", 1, time.Minute)
	if code, _, _ := httpDo(t, http.MethodGet, put, nil); code != 403 { // método errado
		t.Fatalf("wrong method = %d", code)
	}
	now = now.Add(2 * time.Minute)
	if code, _, _ := httpDo(t, http.MethodPut, put, []byte("x")); code != 403 {
		t.Fatalf("expired = %d", code)
	}
}

// TestS3Contract fala com um S3 de verdade (SeaweedFS do compose): KURZ_TEST_S3_ENDPOINT=localhost:8333 (make s3-up).
func TestS3Contract(t *testing.T) {
	ep := os.Getenv("KURZ_TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("KURZ_TEST_S3_ENDPOINT not set: S3 real não testado (make s3-up)")
	}
	ctx := context.Background()
	b := make([]byte, 5)
	_, _ = crand.Read(b)
	s, err := NewS3(ctx, S3Config{Endpoint: ep, Bucket: "kurz-test-" + hex.EncodeToString(b), AccessKey: "kurzdev", SecretKey: "kurzdev-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveBucket(ctx) })
	contract(t, s)
	// o bucket já existente é reaproveitado
	if _, err := NewS3(ctx, S3Config{Endpoint: ep, Bucket: s.bucket, AccessKey: "kurzdev", SecretKey: "kurzdev-secret"}); err != nil {
		t.Fatal(err)
	}
	// o presign usa o host público
	pub, err := NewS3(ctx, S3Config{Endpoint: ep, PublicEndpoint: "http://files.example.com:9000", Bucket: s.bucket, AccessKey: "kurzdev", SecretKey: "kurzdev-secret"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := pub.PresignGet(ctx, "x", GetOptions{}, time.Minute)
	if !strings.HasPrefix(u, "http://files.example.com:9000/") {
		t.Fatalf("public url = %s", u)
	}
}
