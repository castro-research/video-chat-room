package main

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	var v []time.Duration
	for i := 1; i <= 100; i++ {
		v = append(v, time.Duration(i)*time.Millisecond)
	}
	for p, want := range map[float64]int{.50: 50, .95: 95, .99: 99, 1: 100} {
		if got := percentile(v, p); got != time.Duration(want)*time.Millisecond {
			t.Errorf("p%v = %v, want %dms", p, got, want)
		}
	}
	if percentile(nil, .95) != 0 || percentile(v[:1], .99) != time.Millisecond {
		t.Error("edge cases")
	}
}

func TestVerdict(t *testing.T) {
	ok := stats{N: 10, P95: 10 * time.Millisecond, P99: 20 * time.Millisecond}
	if !allOK(verdict(ok, ok, ok, 0, 0, 10, false, 0, 0)) {
		t.Fatal("healthy run should pass")
	}
	slow := ok
	slow.P95 = 200 * time.Millisecond
	if allOK(verdict(slow, ok, ok, 0, 0, 10, false, 0, 0)) {
		t.Fatal("e2e p95 200ms violates RNF1")
	}
	slowRead := ok
	slowRead.P95 = 60 * time.Millisecond
	if allOK(verdict(ok, slowRead, ok, 0, 0, 10, false, 0, 0)) {
		t.Fatal("read p95 60ms violates RNF2")
	}
	if allOK(verdict(ok, ok, ok, 1, 0, 10, false, 0, 0)) || allOK(verdict(ok, ok, ok, 0, 1, 10, false, 0, 0)) {
		t.Fatal("loss and errors fail outside chaos")
	}
	if !allOK(verdict(ok, ok, ok, 5, 5, 10, true, 4, 4)) || allOK(verdict(ok, ok, ok, 0, 0, 10, true, 3, 4)) {
		t.Fatal("chaos tolerates loss but needs every socket back")
	}
	if allOK(verdict(stats{}, ok, ok, 0, 0, 0, false, 0, 0)) {
		t.Fatal("no samples cannot pass")
	}
}

func TestBackoffBoundsAndJitter(t *testing.T) {
	seen := map[time.Duration]bool{}
	for n := 0; n < 40; n++ {
		for i := 0; i < 50; i++ {
			d := backoff(n, 100*time.Millisecond, 2*time.Second)
			if d < 0 || d > 2*time.Second || (n == 0 && d > 100*time.Millisecond) {
				t.Fatalf("n=%d d=%v out of bounds", n, d)
			}
			seen[d] = true
		}
	}
	if len(seen) < 100 {
		t.Fatal("backoff has no jitter")
	}
}
