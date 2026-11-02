package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/ids"
)

const (
	chanWake   = "kurz_outbox_wake"  // Enqueue acorda o relay
	chanEvents = "kurz_events"       // o relay anuncia os eventos publicados
	relayLock  = 0x6b75727a_6f757462 // advisory lock: um relay por vez mantém a ordem global de id

	// NOTIFY aceita ~8000 bytes; acima disso o anúncio leva só a referência e o assinante lê a linha.
	maxInlineNotify = 7000
	// catchUpSlack cobre relays que commitaram fora de ordem de published_at.
	catchUpSlack = 5 * time.Second
	seenCap      = 8192
	retention    = time.Hour
)

// Options ajusta o bus Postgres. Zero usa os padrões.
type Options struct {
	// Sweep é a varredura periódica de pendentes, rede de segurança caso um wake se perca (padrão 500 ms).
	Sweep time.Duration
	// Batch é o máximo de eventos por transação do relay (padrão 200).
	Batch int
}

// Postgres implementa Bus com outbox + LISTEN/NOTIFY.
//
// Fluxo: a escrita grava a linha do outbox na sua transação (Enqueue, que também faz NOTIFY de wake no commit);
// o relay lê os pendentes com FOR UPDATE SKIP LOCKED, marca published_at e emite NOTIFY de evento na MESMA
// transação (o NOTIFY só sai no commit: ou tudo acontece, ou nada e o evento segue pendente). Cada processo
// escuta com uma conexão dedicada e despacha para os assinantes locais.
type Postgres struct {
	pool *pgxpool.Pool
	gen  *ids.Generator
	opt  Options

	mu      sync.RWMutex
	subs    map[int]Handler
	nextSub int

	wake chan struct{}
	// ready fecha quando o listener fez o primeiro LISTEN: o relay só começa depois, para os eventos
	// que ele publicar chegarem aos assinantes deste mesmo processo.
	ready     chan struct{}
	readyOnce sync.Once

	// estado do listener (só a goroutine do listener mexe)
	lastPub time.Time
	seen    map[int64]struct{}
	seenQ   []int64
}

// NewPostgres cria o bus. gen só é usado por Publish quando o evento vem sem id.
func NewPostgres(pool *pgxpool.Pool, gen *ids.Generator, opt Options) *Postgres {
	if opt.Sweep <= 0 {
		opt.Sweep = 500 * time.Millisecond
	}
	if opt.Batch <= 0 {
		opt.Batch = 200
	}
	return &Postgres{pool: pool, gen: gen, opt: opt, subs: map[int]Handler{}, wake: make(chan struct{}, 1), ready: make(chan struct{}), seen: map[int64]struct{}{}}
}

// Enqueue grava o evento no outbox dentro da transação q. O id é obrigatório (use o gerador do domínio).
// Só depois do commit o relay enxerga a linha; se a transação desfizer, o evento some junto com a escrita.
func Enqueue(ctx context.Context, q Execer, e Event) error {
	if e.ID == 0 {
		return errNoID
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO outbox (id, type, guild_id, channel_id, payload) VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0), $5)`,
		e.ID, e.Type, e.GuildID, e.ChannelID, []byte(e.Payload)); err != nil {
		return fmt.Errorf("enqueue %s: %w", e.Type, err)
	}
	_, err := q.Exec(ctx, `SELECT pg_notify($1, '')`, chanWake)
	return err
}

func (b *Postgres) Publish(ctx context.Context, e Event) error {
	if e.ID == 0 {
		if b.gen == nil {
			return errNoID
		}
		e.ID = b.gen.Next()
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := Enqueue(ctx, tx, e); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	b.poke()
	return nil
}

func (b *Postgres) Subscribe(h Handler) (cancel func()) {
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	b.subs[id] = h
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

func (b *Postgres) poke() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Run roda o relay e o listener até ctx terminar. Erros transitórios são tentados de novo com backoff.
func (b *Postgres) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); b.relayLoop(ctx) }()
	go func() { defer wg.Done(); b.listenLoop(ctx) }()
	wg.Wait()
}

// ---- relay ----

func (b *Postgres) relayLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-b.ready:
	}
	sweep := time.NewTicker(b.opt.Sweep)
	defer sweep.Stop()
	lastPurge := time.Now()
	for {
		for ctx.Err() == nil {
			n, err := b.relayBatch(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("outbox relay", "err", err)
				}
				break
			}
			if n < b.opt.Batch {
				break
			}
		}
		if time.Since(lastPurge) > time.Minute {
			lastPurge = time.Now()
			if _, err := b.pool.Exec(ctx, `DELETE FROM outbox WHERE published_at < now() - $1::interval`, retention.String()); err != nil && ctx.Err() == nil {
				slog.Warn("outbox purge", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		case <-sweep.C:
		}
	}
}

type envelope struct {
	Event *Event    `json:"e,omitempty"`
	Ref   int64     `json:"ref,omitempty"`
	Pub   time.Time `json:"pub"`
}

// relayBatch publica até Batch eventos pendentes numa transação. Devolve quantos publicou.
func (b *Postgres) relayBatch(ctx context.Context) (int, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Um relay por vez: preserva a ordem de id entre réplicas. Quem não pega o lock deixa para quem pegou.
	var got bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(relayLock)).Scan(&got); err != nil || !got {
		return 0, err
	}
	rows, err := tx.Query(ctx,
		`SELECT id, type, COALESCE(guild_id, 0), COALESCE(channel_id, 0), payload, created_at, now()
		 FROM outbox WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, b.opt.Batch)
	if err != nil {
		return 0, err
	}
	var evs []Event
	var pub time.Time
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Type, &e.GuildID, &e.ChannelID, &payload, &e.CreatedAt, &pub); err != nil {
			rows.Close()
			return 0, err
		}
		e.Payload = payload
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(evs) == 0 {
		return 0, err
	}
	idList := make([]int64, len(evs))
	for i, e := range evs {
		idList[i] = e.ID
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = $2 WHERE id = ANY($1)`, idList, pub); err != nil {
		return 0, err
	}
	for i := range evs {
		env := envelope{Event: &evs[i], Pub: pub}
		raw, err := json.Marshal(env)
		if err != nil {
			return 0, err
		}
		if len(raw) > maxInlineNotify {
			raw, _ = json.Marshal(envelope{Ref: evs[i].ID, Pub: pub})
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, chanEvents, string(raw)); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(evs), nil
}

// ---- listener ----

func (b *Postgres) listenLoop(ctx context.Context) {
	const minBackoff, maxBackoff = 100 * time.Millisecond, 5 * time.Second
	backoff := minBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := b.listenOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > 10*time.Second {
			backoff = minBackoff
		}
		slog.Warn("bus listener disconnected", "err", err, "retry_in", backoff)
		// jitter para réplicas não reconectarem em bloco
		sleep := backoff/2 + time.Duration(rand.Int64N(int64(backoff)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (b *Postgres) listenOnce(ctx context.Context) error {
	cfg := b.pool.Config().ConnConfig.Copy()
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = ListenerAppName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	for _, ch := range []string{chanWake, chanEvents} {
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return err
		}
	}
	if b.lastPub.IsZero() {
		// só interessam os eventos publicados daqui para a frente
		if err := conn.QueryRow(ctx, `SELECT now()`).Scan(&b.lastPub); err != nil {
			return err
		}
	} else if err := b.catchUp(ctx, conn); err != nil {
		return err
	}
	b.readyOnce.Do(func() { close(b.ready) })
	b.poke() // pode haver pendentes de antes de escutarmos
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		switch n.Channel {
		case chanWake:
			b.poke()
		case chanEvents:
			if err := b.onEventNotify(ctx, n.Payload); err != nil {
				slog.Warn("bus notify", "err", err)
			}
		}
	}
}

// ListenerAppName identifica a conexão de LISTEN no Postgres (útil em pg_stat_activity e nos testes).
const ListenerAppName = "kurz-bus-listener"

func (b *Postgres) onEventNotify(ctx context.Context, payload string) error {
	var env envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return err
	}
	if env.Event == nil {
		e, err := b.load(ctx, env.Ref)
		if err != nil {
			return err
		}
		env.Event = &e
	}
	b.deliver(ctx, *env.Event, env.Pub)
	return nil
}

func (b *Postgres) load(ctx context.Context, id int64) (Event, error) {
	var e Event
	var payload []byte
	err := b.pool.QueryRow(ctx,
		`SELECT id, type, COALESCE(guild_id, 0), COALESCE(channel_id, 0), payload, created_at FROM outbox WHERE id = $1`, id).
		Scan(&e.ID, &e.Type, &e.GuildID, &e.ChannelID, &payload, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, fmt.Errorf("outbox row %d is gone", id)
	}
	e.Payload = payload
	return e, err
}

// catchUp reentrega o que foi publicado enquanto o listener estava fora. Duplicatas são filtradas por id.
func (b *Postgres) catchUp(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx,
		`SELECT id, type, COALESCE(guild_id, 0), COALESCE(channel_id, 0), payload, created_at, published_at
		 FROM outbox WHERE published_at >= $1 ORDER BY published_at, id`, b.lastPub.Add(-catchUpSlack))
	if err != nil {
		return err
	}
	type item struct {
		e   Event
		pub time.Time
	}
	var items []item
	for rows.Next() {
		var it item
		var payload []byte
		if err := rows.Scan(&it.e.ID, &it.e.Type, &it.e.GuildID, &it.e.ChannelID, &payload, &it.e.CreatedAt, &it.pub); err != nil {
			rows.Close()
			return err
		}
		it.e.Payload = payload
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		b.deliver(ctx, it.e, it.pub)
	}
	return nil
}

// deliver despacha para os assinantes, uma vez por id (as duplicatas do catch-up morrem aqui).
func (b *Postgres) deliver(ctx context.Context, e Event, pub time.Time) {
	if pub.After(b.lastPub) {
		b.lastPub = pub
	}
	if _, dup := b.seen[e.ID]; dup {
		return
	}
	b.seen[e.ID] = struct{}{}
	b.seenQ = append(b.seenQ, e.ID)
	if len(b.seenQ) > seenCap {
		delete(b.seen, b.seenQ[0])
		b.seenQ = b.seenQ[1:]
	}
	b.mu.RLock()
	hs := make([]Handler, 0, len(b.subs))
	for _, h := range b.subs {
		hs = append(hs, h)
	}
	b.mu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bus handler panic", "type", e.Type, "panic", r)
				}
			}()
			h(ctx, e)
		}()
	}
}
