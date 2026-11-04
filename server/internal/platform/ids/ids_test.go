package ids

import (
	"testing"
	"time"
)

func TestNextIsUniqueAndIncreasing(t *testing.T) {
	g := NewGenerator(1)
	prev := g.Next()
	seen := map[int64]bool{prev: true}
	for i := 0; i < 100000; i++ {
		id := g.Next()
		if id <= prev {
			t.Fatalf("id %d is not greater than previous %d", id, prev)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
		prev = id
	}
}

func TestNextSurvivesClockGoingBackwards(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	g := newGenerator(0, func() time.Time { return now })
	a := g.Next()
	now = now.Add(-time.Hour)
	b := g.Next()
	if b <= a {
		t.Fatalf("id went backwards: %d then %d", a, b)
	}
}

func TestNextBorrowsNextMillisecondOnSequenceOverflow(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	g := newGenerator(0, func() time.Time { return now })
	prev := g.Next()
	for i := 0; i < 5000; i++ { // mais que 4096 no mesmo milissegundo
		id := g.Next()
		if id <= prev {
			t.Fatalf("id %d is not greater than previous %d at i=%d", id, prev, i)
		}
		prev = id
	}
}

func TestNodeBitsAreEmbedded(t *testing.T) {
	g := NewGenerator(37)
	id := g.Next()
	if got := (id >> seqBits) & MaxNode; got != 37 {
		t.Fatalf("node bits = %d, want 37", got)
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	id := NewGenerator(3).Next()
	got, err := Parse(Format(id))
	if err != nil || got != id {
		t.Fatalf("Parse(Format(%d)) = %d, %v", id, got, err)
	}
	for _, bad := range []string{"", "abc", "-5", "0", "1.5"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}
