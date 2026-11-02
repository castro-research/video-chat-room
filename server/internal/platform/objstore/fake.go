package objstore

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fake guarda os objetos em memória. Serve também como http.Handler (monte num httptest.Server e chame
// SetBaseURL) para que os testes façam PUT/GET de verdade nas URLs "pré-assinadas", que carregam expiração e método.
type Fake struct {
	mu      sync.Mutex
	objs    map[string]fakeObj
	base    string
	now     func() time.Time
	deleted []string
}

type fakeObj struct {
	data        []byte
	contentType string
}

func NewFake() *Fake {
	return &Fake{objs: map[string]fakeObj{}, base: "http://fake.invalid", now: time.Now}
}

func (f *Fake) SetBaseURL(u string) { f.mu.Lock(); f.base = strings.TrimRight(u, "/"); f.mu.Unlock() }

// SetClock troca o relógio usado para validar a expiração das URLs.
func (f *Fake) SetClock(now func() time.Time) { f.mu.Lock(); f.now = now; f.mu.Unlock() }

func (f *Fake) sign(method, key string, ttl time.Duration, extra url.Values) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	q.Set("method", method)
	q.Set("exp", strconv.FormatInt(f.now().Add(ttl).Unix(), 10))
	return f.base + "/" + key + "?" + q.Encode()
}

func (f *Fake) PresignPut(_ context.Context, key string, ttl time.Duration) (string, error) {
	return f.sign(http.MethodPut, key, ttl, nil), nil
}

func (f *Fake) PresignGet(_ context.Context, key string, opt GetOptions, ttl time.Duration) (string, error) {
	q := url.Values{}
	if opt.ContentType != "" {
		q.Set("response-content-type", opt.ContentType)
	}
	if opt.Filename != "" {
		q.Set("filename", opt.Filename)
	}
	return f.sign(http.MethodGet, key, ttl, q), nil
}

func (f *Fake) Stat(_ context.Context, key string) (Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objs[key]
	if !ok {
		return Info{}, ErrNotFound
	}
	return Info{Size: int64(len(o.data)), ContentType: o.contentType}, nil
}

func (f *Fake) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objs, key)
	f.deleted = append(f.deleted, key)
	return nil
}

// Put grava direto, sem passar pelo HTTP.
func (f *Fake) Put(key string, data []byte, contentType string) {
	f.mu.Lock()
	f.objs[key] = fakeObj{data: data, contentType: contentType}
	f.mu.Unlock()
}

func (f *Fake) Has(key string) bool { f.mu.Lock(); defer f.mu.Unlock(); _, ok := f.objs[key]; return ok }

func (f *Fake) Count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.objs) }

func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objs))
	for k := range f.objs {
		out = append(out, k)
	}
	return out
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/")
	q := r.URL.Query()
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	f.mu.Lock()
	now := f.now()
	f.mu.Unlock()
	if err != nil || q.Get("method") != r.Method || now.Unix() > exp {
		http.Error(w, "signature invalid or expired", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPut:
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.Put(key, b, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		f.mu.Lock()
		o, ok := f.objs[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		ct := q.Get("response-content-type")
		if ct == "" {
			ct = o.contentType
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(o.data)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
