package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/ids"
)

func newWorker(t *testing.T, opt Options) (*Worker, *ids.Generator, func(kind string, payload any, at time.Time)) {
	t.Helper()
	pool := dbtest.New(t)
	gen := ids.NewGenerator(1)
	w := NewWorker(pool, gen, opt)
	enq := func(kind string, payload any, at time.Time) {
		t.Helper()
		if err := Enqueue(context.Background(), pool, gen.Next(), kind, payload, at); err != nil {
			t.Fatal(err)
		}
	}
	return w, gen, enq
}

func TestRunOnceHandlesAndMarksDone(t *testing.T) {
	ctx := context.Background()
	w, _, enq := newWorker(t, Options{})
	var got []string
	w.Handle("echo", func(_ context.Context, p json.RawMessage) error {
		var v struct{ N string }
		_ = json.Unmarshal(p, &v)
		got = append(got, v.N)
		return nil
	})
	enq("echo", map[string]string{"N": "a"}, time.Time{})
	enq("echo", map[string]string{"N": "b"}, time.Time{})
	enq("echo", map[string]string{"N": "later"}, time.Now().Add(time.Hour)) // ainda não é hora
	n, err := w.RunOnce(ctx)
	if err != nil || n != 2 || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("n=%d got=%v err=%v", n, got, err)
	}
	var done int
	_ = w.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE done_at IS NOT NULL`).Scan(&done)
	if done != 2 {
		t.Fatalf("done = %d", done)
	}
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("second run = %d", n)
	}
}

func TestRetryWithBackoffThenFail(t *testing.T) {
	ctx := context.Background()
	w, _, enq := newWorker(t, Options{MaxAttempts: 3, Backoff: func(int) time.Duration { return 0 }})
	var calls atomic.Int32
	w.Handle("boom", func(context.Context, json.RawMessage) error { calls.Add(1); return errors.New("nope") })
	w.Handle("flaky", func(context.Context, json.RawMessage) error {
		if calls.Add(1) < 3 { // falha duas vezes e passa na terceira
			return errors.New("transient")
		}
		return nil
	})
	enq("boom", nil, time.Time{})
	if n, err := w.RunOnce(ctx); err != nil || n != 3 || calls.Load() != 3 {
		t.Fatalf("n=%d calls=%d err=%v", n, calls.Load(), err)
	}
	var failed int
	var lastErr string
	_ = w.pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(last_error), '') FROM jobs WHERE failed_at IS NOT NULL AND attempts = 3`).Scan(&failed, &lastErr)
	if failed != 1 || lastErr != "nope" {
		t.Fatalf("failed=%d last_error=%q", failed, lastErr)
	}
	if n, _ := w.RunOnce(ctx); n != 0 { // job falho não volta
		t.Fatalf("failed job ran again: %d", n)
	}
	calls.Store(0)
	enq("flaky", nil, time.Time{})
	if n, _ := w.RunOnce(ctx); n != 3 {
		t.Fatalf("flaky runs = %d", n)
	}
	var done int
	_ = w.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'flaky' AND done_at IS NOT NULL`).Scan(&done)
	if done != 1 {
		t.Fatal("flaky must end done")
	}
}

func TestBackoffGrowsAndSchedulesInTheFuture(t *testing.T) {
	if defaultBackoff(1) != time.Second || defaultBackoff(3) != 4*time.Second || defaultBackoff(50) != 10*time.Minute {
		t.Fatalf("backoff = %v %v %v", defaultBackoff(1), defaultBackoff(3), defaultBackoff(50))
	}
	ctx := context.Background()
	w, _, enq := newWorker(t, Options{Backoff: func(int) time.Duration { return time.Hour }})
	w.Handle("x", func(context.Context, json.RawMessage) error { return errors.New("e") })
	enq("x", nil, time.Time{})
	if n, _ := w.RunOnce(ctx); n != 1 {
		t.Fatalf("n = %d", n)
	}
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("job must wait for its backoff, ran %d", n)
	}
}

func TestUnknownKindAndPanicAreRetriedNotFatal(t *testing.T) {
	ctx := context.Background()
	w, _, enq := newWorker(t, Options{MaxAttempts: 1})
	w.Handle("panic", func(context.Context, json.RawMessage) error { panic("ai") })
	enq("panic", nil, time.Time{})
	enq("ghost", nil, time.Time{})
	if n, err := w.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var failed int
	_ = w.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE failed_at IS NOT NULL`).Scan(&failed)
	if failed != 2 {
		t.Fatalf("failed = %d", failed)
	}
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	ctx := context.Background()
	w, _, enq := newWorker(t, Options{Lease: time.Minute})
	var calls atomic.Int32
	w.Handle("k", func(context.Context, json.RawMessage) error { calls.Add(1); return nil })
	enq("k", nil, time.Time{})
	// simula um worker que morreu no meio: aluguel recente, não pode ser pego; aluguel velho, pode
	_, _ = w.pool.Exec(ctx, `UPDATE jobs SET locked_at = now(), attempts = 1`)
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("leased job must be skipped, ran %d", n)
	}
	_, _ = w.pool.Exec(ctx, `UPDATE jobs SET locked_at = now() - interval '2 minutes'`)
	if n, _ := w.RunOnce(ctx); n != 1 || calls.Load() != 1 {
		t.Fatalf("expired lease must be reclaimed, ran %d", n)
	}
}

// Vários workers disputando a mesma fila: cada job roda exatamente uma vez.
func TestSkipLockedNoDoubleProcessing(t *testing.T) {
	ctx := context.Background()
	w, gen, enq := newWorker(t, Options{})
	const jobs = 60
	for i := 0; i < jobs; i++ {
		enq("count", i, time.Time{})
	}
	var mu sync.Mutex
	seen := map[string]int{}
	h := func(_ context.Context, p json.RawMessage) error {
		mu.Lock()
		seen[string(p)]++
		mu.Unlock()
		time.Sleep(time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		other := NewWorker(w.pool, gen, Options{})
		other.Handle("count", h)
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = other.RunOnce(ctx) }()
	}
	wg.Wait()
	if len(seen) != jobs {
		t.Fatalf("processed %d distinct jobs, want %d", len(seen), jobs)
	}
	for p, n := range seen {
		if n != 1 {
			t.Fatalf("job %s ran %d times", p, n)
		}
	}
}

func TestRunLoopStopsOnCancelAndPeriodicEnqueues(t *testing.T) {
	w, _, _ := newWorker(t, Options{Poll: 10 * time.Millisecond})
	ran := make(chan struct{}, 10)
	w.Handle("tick", func(context.Context, json.RawMessage) error { ran <- struct{}{}; return nil })
	w.Every("tick", 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("periodic job did not run")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
