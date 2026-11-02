// Package jobs é uma fila de trabalhos em segundo plano sobre o Postgres (tabela `jobs`).
//
// Quem escreve enfileira DENTRO da própria transação (Enqueue), como no outbox: o job só existe se a escrita
// foi confirmada. O Worker pega com FOR UPDATE SKIP LOCKED, então várias réplicas dividem a fila sem se atropelar.
// A entrega é at-least-once: o job pego fica "alugado" (locked_at) e, se o processo morrer, o aluguel expira e outro
// worker repete. Os handlers precisam ser idempotentes.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/ids"
)

// Execer é o que Enqueue precisa: pgx.Tx, *pgxpool.Pool e *pgx.Conn servem.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Enqueue insere um job para rodar já (runAt zero) ou em runAt.
func Enqueue(ctx context.Context, q Execer, id int64, kind string, payload any, runAt time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var at *time.Time // nil = agora, pelo relógio do banco (o do processo pode divergir dele)
	if !runAt.IsZero() {
		at = &runAt
	}
	_, err = q.Exec(ctx, `INSERT INTO jobs (id, kind, payload, run_at) VALUES ($1, $2, $3, COALESCE($4, now()))`, id, kind, b, at)
	return err
}

// Handler executa um job. Retornar erro agenda nova tentativa com backoff.
type Handler func(ctx context.Context, payload json.RawMessage) error

type Options struct {
	Poll        time.Duration                   // intervalo entre varreduras com a fila vazia (padrão 1s)
	Lease       time.Duration                   // quanto tempo um job pego fica reservado (padrão 5min)
	MaxAttempts int                             // depois disso o job vira failed (padrão 8)
	Backoff     func(attempt int) time.Duration // espera antes da tentativa seguinte (padrão exponencial, 1s a 10min)
	Concurrency int                             // goroutines pegando jobs (padrão 2)
}

func (o Options) withDefaults() Options {
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	if o.Lease <= 0 {
		o.Lease = 5 * time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 8
	}
	if o.Backoff == nil {
		o.Backoff = defaultBackoff
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}
	return o
}

func defaultBackoff(attempt int) time.Duration {
	d := time.Second << min(max(attempt-1, 0), 10)
	return min(d, 10*time.Minute)
}

type periodic struct {
	kind  string
	every time.Duration
}

type Worker struct {
	pool *pgxpool.Pool
	gen  *ids.Generator
	opt  Options

	mu       sync.Mutex
	handlers map[string]Handler
	periodic []periodic
}

func NewWorker(pool *pgxpool.Pool, gen *ids.Generator, opt Options) *Worker {
	return &Worker{pool: pool, gen: gen, opt: opt.withDefaults(), handlers: map[string]Handler{}}
}

// Handle registra o handler de um kind. Registre tudo antes de Run.
func (w *Worker) Handle(kind string, h Handler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[kind] = h
}

// Every faz o worker enfileirar `kind` a cada intervalo, sem acumular: só enfileira se não houver um pendente.
// Réplicas que chamam ao mesmo tempo podem enfileirar um a mais numa corrida rara; o handler periódico é idempotente.
func (w *Worker) Every(kind string, interval time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.periodic = append(w.periodic, periodic{kind, interval})
}

type claimed struct {
	id       int64
	kind     string
	payload  json.RawMessage
	attempts int
}

// claim reserva o próximo job pronto. O aluguel é gravado na hora (commit), então o handler roda fora de transação.
func (w *Worker) claim(ctx context.Context) (*claimed, error) {
	var c claimed
	err := w.pool.QueryRow(ctx, `
		UPDATE jobs SET locked_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT id FROM jobs
			WHERE done_at IS NULL AND failed_at IS NULL AND run_at <= now()
			  AND (locked_at IS NULL OR locked_at < now() - make_interval(secs => $1))
			ORDER BY run_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1)
		RETURNING id, kind, payload, attempts`, w.opt.Lease.Seconds()).Scan(&c.id, &c.kind, &c.payload, &c.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (w *Worker) finish(ctx context.Context, c *claimed, runErr error) error {
	switch {
	case runErr == nil:
		_, err := w.pool.Exec(ctx, `UPDATE jobs SET done_at = now(), locked_at = NULL, last_error = NULL WHERE id = $1`, c.id)
		return err
	case c.attempts >= w.opt.MaxAttempts:
		slog.Error("job failed for good", "kind", c.kind, "id", c.id, "attempts", c.attempts, "err", runErr)
		_, err := w.pool.Exec(ctx, `UPDATE jobs SET failed_at = now(), locked_at = NULL, last_error = $2 WHERE id = $1`, c.id, runErr.Error())
		return err
	default:
		_, err := w.pool.Exec(ctx,
			`UPDATE jobs SET locked_at = NULL, last_error = $2, run_at = now() + make_interval(secs => $3) WHERE id = $1`,
			c.id, runErr.Error(), w.opt.Backoff(c.attempts).Seconds())
		return err
	}
}

func (w *Worker) run(ctx context.Context, c *claimed) {
	w.mu.Lock()
	h, ok := w.handlers[c.kind]
	w.mu.Unlock()
	var err error
	if !ok {
		err = fmt.Errorf("no handler for kind %q", c.kind)
	} else {
		err = safely(ctx, h, c.payload)
	}
	// o resultado é gravado mesmo com o ctx cancelado (shutdown), senão o job rodaria de novo sem necessidade
	fin, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if ferr := w.finish(fin, c, err); ferr != nil {
		slog.Error("job finish", "id", c.id, "err", ferr)
	}
}

func safely(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, payload)
}

// RunOnce executa os jobs prontos até a fila esvaziar e devolve quantos rodou. Útil em testes.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	n := 0
	for {
		c, err := w.claim(ctx)
		if err != nil || c == nil {
			return n, err
		}
		w.run(ctx, c)
		n++
	}
}

// Run processa a fila até ctx terminar e só retorna quando os jobs em curso acabarem.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.opt.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				c, err := w.claim(ctx)
				if err != nil {
					if ctx.Err() == nil {
						slog.Error("job claim", "err", err)
					}
				}
				if c != nil {
					w.run(ctx, c)
					continue
				}
				select {
				case <-ctx.Done():
				case <-time.After(w.opt.Poll):
				}
			}
		}()
	}
	w.mu.Lock()
	ps := append([]periodic(nil), w.periodic...)
	w.mu.Unlock()
	for _, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(p.every)
			defer t.Stop()
			for {
				if err := w.enqueueOnce(ctx, p.kind); err != nil && ctx.Err() == nil {
					slog.Error("periodic enqueue", "kind", p.kind, "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := w.Purge(ctx, 24*time.Hour); err != nil && ctx.Err() == nil {
					slog.Error("jobs purge", "err", err)
				}
			}
		}
	}()
	wg.Wait()
}

// enqueueOnce enfileira kind se não houver um pendente.
func (w *Worker) enqueueOnce(ctx context.Context, kind string) error {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO jobs (id, kind, payload)
		SELECT $1, $2, '{}' WHERE NOT EXISTS (
			SELECT 1 FROM jobs WHERE kind = $2 AND done_at IS NULL AND failed_at IS NULL)
		`, w.gen.Next(), kind)
	return err
}

// Purge apaga jobs concluídos há mais de olderThan. O Run chama de hora em hora com 24h.
func (w *Worker) Purge(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := w.pool.Exec(ctx, `DELETE FROM jobs WHERE done_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	return tag.RowsAffected(), err
}
