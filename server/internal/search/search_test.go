package search

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/dbtest"
)

type fakePerms struct{ channels []int64 }

func (f fakePerms) VisibleTextChannels(context.Context, int64, int64) ([]int64, error) {
	return f.channels, nil
}

const (
	benchGuild    = 1
	benchChannels = 20
	benchAuthors  = 50
	benchVocab    = 5000
)

// seed gera n mensagens com vocabulário de cauda longa: w0 é comum, w4999 é raro (uso de `power(random(), 3)`
// concentra a massa nas palavras de índice baixo, como numa língua real). IDs crescentes, 20 canais, 50 autores.
func seed(t testing.TB, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, username, name, password_hash) SELECT g, 'u' || g, 'User ' || g, 'x' FROM generate_series(1, $1) g`, benchAuthors)
	mustExec(`INSERT INTO guilds (id, name, owner_id) VALUES ($1, 'g', 1)`, benchGuild)
	mustExec(`INSERT INTO channels (id, guild_id, kind, name, position) SELECT 1000 + g, $1, 'text', 'c' || g, g FROM generate_series(1, $2) g`, benchGuild, benchChannels)
	start := time.Now()
	mustExec(fmt.Sprintf(`
		INSERT INTO messages (id, channel_id, author_id, content)
		SELECT g, 1000 + 1 + (g %% %d), 1 + (g %% %d),
		       (SELECT string_agg('w' || floor(power(random(), 3) * %d)::int, ' ') FROM generate_series(1, 8) WHERE g > 0)
		FROM generate_series(1, $1) g`, benchChannels, benchAuthors, benchVocab), n)
	mustExec(`VACUUM ANALYZE messages`) // esvazia a pending list do GIN e atualiza estatísticas
	t.Logf("seeded %d messages in %s", n, time.Since(start).Round(time.Millisecond))
}

func visibleChannels(n int) []int64 {
	var out []int64
	for i := 1; i <= n; i++ {
		out = append(out, int64(1000+i))
	}
	return out
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(math.Ceil(p*float64(len(sorted))))-1]
}

// TestSearchSmallVolume: sanidade com 50k mensagens (roda no `make test`; pula com -short).
func TestSearchSmallVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: pulando o volume pequeno de busca")
	}
	pool := dbtest.New(t)
	seed(t, pool, 50_000)
	svc := NewService(pool, fakePerms{visibleChannels(15)})
	ctx := context.Background()
	p, err := svc.Search(ctx, 1, benchGuild, Query{Text: "w5 w9", Limit: 10, Highlight: true})
	if err != nil || len(p.Items) != 10 || p.NextCursor == 0 {
		t.Fatalf("page = %d items next=%d err=%v", len(p.Items), p.NextCursor, err)
	}
	for _, it := range p.Items {
		if it.ChannelID > 1015 || !strings.Contains(it.Highlight, HighlightStart) {
			t.Fatalf("item outside visible channels or without highlight: %+v", it)
		}
	}
}

// TestSearchVolume mede a latência da busca em volume (RNF3: p95 < 300 ms em 1M mensagens).
// Só roda com KURZ_BENCH_SEARCH=1 (make bench-search). KURZ_BENCH_MESSAGES muda o volume (padrão 1.000.000).
func TestSearchVolume(t *testing.T) {
	if os.Getenv("KURZ_BENCH_SEARCH") == "" {
		t.Skip("KURZ_BENCH_SEARCH not set: teste de volume pulado (make bench-search)")
	}
	n := 1_000_000
	if v := os.Getenv("KURZ_BENCH_MESSAGES"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	pool := dbtest.New(t)
	seed(t, pool, n)
	svc := NewService(pool, fakePerms{visibleChannels(15)}) // vê 15 dos 20 canais
	ctx := context.Background()

	type scenario struct {
		name string
		mk   func(i int) Query
	}
	word := func(i int, pool []int) string { return "w" + strconv.Itoa(pool[i%len(pool)]) }
	common := []int{0, 1, 2, 3, 5, 8}                 // casam muitas mensagens
	medium := []int{40, 90, 150, 300, 500, 800}       // centenas a milhares
	rare := []int{2500, 3200, 3900, 4400, 4800, 4990} // poucas
	scenarios := []scenario{
		{"palavra comum", func(i int) Query { return Query{Text: word(i, common), Limit: 25} }},
		{"palavra média", func(i int) Query { return Query{Text: word(i, medium), Limit: 25} }},
		{"palavra rara", func(i int) Query { return Query{Text: word(i, rare), Limit: 25} }},
		{"duas palavras (AND)", func(i int) Query { return Query{Text: word(i, common) + " " + word(i+1, medium), Limit: 25} }},
		{"comum + canal", func(i int) Query { return Query{Text: word(i, common), ChannelID: int64(1001 + i%15), Limit: 25} }},
		{"média + autor", func(i int) Query { return Query{Text: word(i, medium), AuthorID: int64(1 + i%benchAuthors), Limit: 25} }},
		{"comum + highlight", func(i int) Query { return Query{Text: word(i, common), Limit: 25, Highlight: true} }},
		{"comum, página 2", func(i int) Query {
			return Query{Text: word(i, common), Limit: 25, Cursor: int64(n - 5000)}
		}},
		{"frase", func(i int) Query {
			return Query{Text: `"` + word(i, common) + " " + word(i+1, medium) + `"`, Limit: 25}
		}},
	}
	const reps = 60
	var all []time.Duration
	for _, sc := range scenarios {
		var ds []time.Duration
		for i := 0; i < reps; i++ {
			start := time.Now()
			if _, err := svc.Search(ctx, 1, benchGuild, sc.mk(i)); err != nil {
				t.Fatalf("%s: %v", sc.name, err)
			}
			ds = append(ds, time.Since(start))
		}
		all = append(all, ds...)
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("%-22s p50=%-8s p95=%-8s max=%s", sc.name, percentile(ds, .5).Round(time.Microsecond), percentile(ds, .95).Round(time.Microsecond), ds[len(ds)-1].Round(time.Microsecond))
	}
	for _, q := range []Query{{Text: "w40", Limit: 25}, {Text: "w40", AuthorID: 7, Limit: 25}, {Text: "w3200", Limit: 25}} {
		sql, args := buildSQL(q, visibleChannels(15))
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) "+sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			plan = append(plan, line)
		}
		rows.Close()
		t.Logf("EXPLAIN %+v\n%s", q, strings.Join(plan, "\n"))
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	p95 := percentile(all, .95)
	t.Logf("TOTAL (%d msgs, %d consultas) p50=%s p95=%s p99=%s", n, len(all), percentile(all, .5).Round(time.Microsecond), p95.Round(time.Microsecond), percentile(all, .99).Round(time.Microsecond))
	if n >= 1_000_000 && p95 > 300*time.Millisecond {
		t.Errorf("RNF3 violado: p95 = %s > 300ms", p95)
	}
}
