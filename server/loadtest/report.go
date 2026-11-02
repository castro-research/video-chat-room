package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"time"
)

// samples acumula durações de várias goroutines.
type samples struct {
	mu sync.Mutex
	v  []time.Duration
}

func (s *samples) add(d time.Duration) {
	s.mu.Lock()
	s.v = append(s.v, d)
	s.mu.Unlock()
}

func (s *samples) stats() stats {
	s.mu.Lock()
	v := slices.Clone(s.v)
	s.mu.Unlock()
	slices.Sort(v)
	return stats{N: len(v), P50: percentile(v, .50), P95: percentile(v, .95), P99: percentile(v, .99), Max: percentile(v, 1)}
}

type stats struct {
	N                  int
	P50, P95, P99, Max time.Duration
}

func (s stats) String() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s p99=%s max=%s", s.N, ms(s.P50), ms(s.P95), ms(s.P99), ms(s.Max))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }

// percentile devolve o percentil p (0..1) de v ordenado, pelo método do posto mais próximo (nearest-rank).
func percentile(v []time.Duration, p float64) time.Duration {
	if len(v) == 0 {
		return 0
	}
	rank := int(p*float64(len(v)) + 0.9999999)
	return v[min(max(rank, 1), len(v))-1]
}

// check é uma linha do veredito.
type check struct {
	Name string
	Got  string
	Want string
	OK   bool
}

// limits são os alvos de RNF1 e RNF2.
var (
	e2eP95, e2eP99    = 150 * time.Millisecond, 400 * time.Millisecond
	readP95, writeP95 = 50 * time.Millisecond, 100 * time.Millisecond
)

func verdict(e2e, read, write stats, missed, errs, expected int, chaos bool, connected, users int) []check {
	lt := func(name string, got, want time.Duration, n int) check {
		return check{name, ms(got), "< " + ms(want), n > 0 && got < want}
	}
	cs := []check{
		lt("RNF1 e2e p95", e2e.P95, e2eP95, e2e.N),
		lt("RNF1 e2e p99", e2e.P99, e2eP99, e2e.N),
		lt("RNF2 read p95", read.P95, readP95, read.N),
		lt("RNF2 write p95", write.P95, writeP95, write.N),
	}
	if chaos {
		cs = append(cs, check{"RNF8 sockets reconnected", fmt.Sprintf("%d/%d", connected, users), fmt.Sprintf("%d/%d", users, users), connected == users})
	} else {
		cs = append(cs,
			check{"delivery (no event lost)", fmt.Sprintf("%d missed of %d", missed, expected), "0", missed == 0},
			check{"request errors", fmt.Sprint(errs), "0", errs == 0})
	}
	return cs
}

func allOK(cs []check) bool {
	for _, c := range cs {
		if !c.OK {
			return false
		}
	}
	return true
}

// backoff é o atraso antes da tentativa n (0, 1, ...) de reconectar: exponencial com teto e jitter total
// (uniforme em [0, min(cap, base*2^n)]), que dispersa uma manada de clientes que caíram juntos.
func backoff(n int, base, cap time.Duration) time.Duration {
	d := cap
	if n < 20 {
		d = min(cap, base<<n)
	}
	return time.Duration(rand.Int64N(int64(d) + 1))
}
