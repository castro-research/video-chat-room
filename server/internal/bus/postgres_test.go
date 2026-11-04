package bus_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/platform/db"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/ids"
)

const wait = 10 * time.Second

type collector struct {
	mu  sync.Mutex
	got []bus.Event
	ch  chan bus.Event
}

func collect(b bus.Bus) *collector {
	c := &collector{ch: make(chan bus.Event, 1024)}
	b.Subscribe(func(_ context.Context, e bus.Event) {
		c.mu.Lock()
		c.got = append(c.got, e)
		c.mu.Unlock()
		c.ch <- e
	})
	return c
}

func (c *collector) next(t *testing.T) bus.Event {
	t.Helper()
	select {
	case e := <-c.ch:
		return e
	case <-time.After(wait):
		t.Fatal("timed out waiting for an event")
		return bus.Event{}
	}
}

func (c *collector) none(t *testing.T) {
	t.Helper()
	select {
	case e := <-c.ch:
		t.Fatalf("unexpected event %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

// start sobe o bus e devolve uma função que o derruba e espera Run terminar.
func start(t *testing.T, b *bus.Postgres) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// listening espera o listener do bus aparecer no Postgres, para os testes não correrem contra o LISTEN.
func listening(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		var c int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1 AND datname = current_database()`, bus.ListenerAppName).Scan(&c); err != nil {
			t.Fatal(err)
		}
		if c >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("listener never connected")
}

func newBus(t *testing.T, pool *pgxpool.Pool, node int64) *bus.Postgres {
	return bus.NewPostgres(pool, ids.NewGenerator(node), bus.Options{Sweep: 100 * time.Millisecond})
}

func TestPublishReachesSubscriber(t *testing.T) {
	pool := dbtest.New(t)
	b := newBus(t, pool, 1)
	c := collect(b)
	start(t, b)
	listening(t, pool, 1)

	err := b.Publish(context.Background(), bus.Event{Type: bus.MessageCreate, GuildID: 1<<40 + 7, ChannelID: 1<<41 + 9, Payload: json.RawMessage(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	e := c.next(t)
	if e.Type != bus.MessageCreate || e.GuildID != 1<<40+7 || e.ChannelID != 1<<41+9 || strings.ReplaceAll(string(e.Payload), " ", "") != `{"x":1}` || e.ID == 0 {
		t.Fatalf("event = %+v", e)
	}
	var pub *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT published_at FROM outbox WHERE id = $1`, e.ID).Scan(&pub); err != nil || pub == nil {
		t.Fatalf("published_at = %v, err %v", pub, err)
	}
}

// Enqueue dentro de uma transação: rollback não publica nada, commit publica.
func TestEnqueueFollowsTransaction(t *testing.T) {
	pool := dbtest.New(t)
	gen := ids.NewGenerator(1)
	b := newBus(t, pool, 1)
	c := collect(b)
	start(t, b)
	listening(t, pool, 1)
	ctx := context.Background()

	tx, _ := pool.Begin(ctx)
	if err := bus.Enqueue(ctx, tx, bus.Event{ID: gen.Next(), Type: bus.MessageCreate}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	c.none(t)

	tx, _ = pool.Begin(ctx)
	id := gen.Next()
	if err := bus.Enqueue(ctx, tx, bus.Event{ID: id, Type: bus.MessageDelete}); err != nil {
		t.Fatal(err)
	}
	c.none(t) // ainda não commitou
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if e := c.next(t); e.ID != id || e.Type != bus.MessageDelete {
		t.Fatalf("event = %+v", e)
	}

	if err := bus.Enqueue(ctx, pool, bus.Event{Type: "x"}); err == nil {
		t.Fatal("Enqueue without id must fail")
	}
}

// Nenhum evento se perde se o processo cair entre o commit e o NOTIFY: o que ficou pendente
// é publicado quando o bus (re)inicia, uma vez só, em ordem de id.
func TestRelayPublishesPendingAfterRestart(t *testing.T) {
	pool := dbtest.New(t)
	gen := ids.NewGenerator(1)
	ctx := context.Background()

	// "o processo caiu": há eventos commitados e nenhum relay rodando
	var want []int64
	for range 5 {
		id := gen.Next()
		want = append(want, id)
		if err := bus.Enqueue(ctx, pool, bus.Event{ID: id, Type: bus.MessageCreate, ChannelID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	b := newBus(t, pool, 1)
	c := collect(b)
	stop := start(t, b)
	for i, id := range want {
		if e := c.next(t); e.ID != id {
			t.Fatalf("event %d = %d, want %d", i, e.ID, id)
		}
	}
	stop()

	// nova "vida" do processo: nada pendente, nada repetido
	b2 := newBus(t, pool, 2)
	c2 := collect(b2)
	start(t, b2)
	listening(t, pool, 1)
	c2.none(t)
	id := gen.Next()
	if err := bus.Enqueue(ctx, pool, bus.Event{ID: id, Type: bus.MessageUpdate}); err != nil {
		t.Fatal(err)
	}
	if e := c2.next(t); e.ID != id {
		t.Fatalf("event = %d, want %d", e.ID, id)
	}
}

// Dois "nós" (pools e listeners diferentes) no mesmo Postgres: publicar em um chega nos dois.
func TestNotifyAcrossTwoNodes(t *testing.T) {
	poolA := dbtest.New(t)
	poolB, err := db.Open(context.Background(), poolA.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolB.Close)
	a, b := newBus(t, poolA, 1), newBus(t, poolB, 2)
	ca, cb := collect(a), collect(b)
	start(t, a)
	start(t, b)
	listening(t, poolA, 2)

	if err := a.Publish(context.Background(), bus.Event{Type: bus.ChannelCreate, GuildID: 3}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), bus.Event{Type: bus.ChannelDelete, GuildID: 3}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*collector{ca, cb} {
		got := map[string]bool{c.next(t).Type: true, c.next(t).Type: true}
		if !got[bus.ChannelCreate] || !got[bus.ChannelDelete] {
			t.Fatalf("got %v", got)
		}
		c.none(t) // sem duplicatas
	}
}

// Se a conexão de LISTEN cair, o listener reconecta com backoff e reentrega o que perdeu.
func TestListenerReconnectsAndCatchesUp(t *testing.T) {
	pool := dbtest.New(t)
	b := newBus(t, pool, 1)
	c := collect(b)
	start(t, b)
	listening(t, pool, 1)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = $1 AND datname = current_database()`, bus.ListenerAppName); err != nil {
		t.Fatal(err)
	}
	// publicado com o listener fora do ar (ou prestes a perceber): chega depois, pelo catch-up
	if err := b.Publish(ctx, bus.Event{Type: bus.MessageCreate, ChannelID: 5}); err != nil {
		t.Fatal(err)
	}
	if e := c.next(t); e.Type != bus.MessageCreate || e.ChannelID != 5 {
		t.Fatalf("event = %+v", e)
	}
	c.none(t)
	listening(t, pool, 1)
	if err := b.Publish(ctx, bus.Event{Type: bus.MessageDelete}); err != nil {
		t.Fatal(err)
	}
	if e := c.next(t); e.Type != bus.MessageDelete {
		t.Fatalf("event = %+v", e)
	}
}

// Payload maior que o limite do NOTIFY segue por referência.
func TestLargePayload(t *testing.T) {
	pool := dbtest.New(t)
	b := newBus(t, pool, 1)
	c := collect(b)
	start(t, b)
	listening(t, pool, 1)
	big, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", 20000)})
	if err := b.Publish(context.Background(), bus.Event{Type: bus.MessageCreate, Payload: big}); err != nil {
		t.Fatal(err)
	}
	e := c.next(t)
	var got map[string]string
	if err := json.Unmarshal(e.Payload, &got); err != nil || len(got["text"]) != 20000 {
		t.Fatalf("payload len %d, err %v", len(got["text"]), err)
	}
}

// Dois relays concorrentes não duplicam nem perdem eventos.
func TestTwoRelaysNoDuplicates(t *testing.T) {
	poolA := dbtest.New(t)
	poolB, err := db.Open(context.Background(), poolA.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolB.Close)
	a, b := newBus(t, poolA, 1), newBus(t, poolB, 2)
	ca, cb := collect(a), collect(b)
	start(t, a)
	start(t, b)
	listening(t, poolA, 2)
	gen := ids.NewGenerator(3)
	const n = 100
	for range n {
		if err := bus.Enqueue(context.Background(), poolA, bus.Event{ID: gen.Next(), Type: bus.MessageCreate}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*collector{ca, cb} {
		seen := map[int64]bool{}
		for range n {
			e := c.next(t)
			if seen[e.ID] {
				t.Fatalf("duplicate %d", e.ID)
			}
			seen[e.ID] = true
		}
		c.none(t)
	}
}
