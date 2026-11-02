package voice

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

type fakeChannels struct{ speak bool }

func (f fakeChannels) VoiceAccess(_ context.Context, _, ch int64) (Channel, error) {
	switch ch {
	case 100, 101:
		return Channel{ID: ch, GuildID: 10, Voice: true, Connect: true, Speak: f.speak}, nil
	case 102:
		return Channel{ID: ch, GuildID: 10, Voice: false, Connect: true, Speak: true}, nil
	}
	return Channel{}, httpx.ErrNotFound
}

func setup(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users (id, username, name, password_hash) VALUES (1,'a','A','x'), (2,'b','B','x'), (3,'c','C','x')`,
		`INSERT INTO guilds (id, name, owner_id) VALUES (10, 'g', 1)`,
		`INSERT INTO members (guild_id, user_id) VALUES (10,1), (10,2)`,
		`INSERT INTO channels (id, guild_id, kind, name, position) VALUES (100,10,'voice','v1',0), (101,10,'voice','v2',1), (102,10,'text','t',2)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(pool, ids.NewGenerator(1), fakeChannels{speak: true}, Config{Secret: secret, SFUURL: "ws://sfu/v1/media"})
	return svc, pool
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJoinMoveLeaveEmitEvents(t *testing.T) {
	svc, pool := setup(t)
	ctx := context.Background()
	if _, err := svc.Join(ctx, 1, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, 1, 100); err != nil { // reconexão: sem evento novo
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, 1, 101); err != nil { // move: saída + entrada
		t.Fatal(err)
	}
	if err := svc.Leave(ctx, 1, 101); err != nil {
		t.Fatal(err)
	}
	if err := svc.Leave(ctx, 1, 101); err != nil { // idempotente
		t.Fatal(err)
	}
	want := []struct {
		ch   int64
		conn string
	}{{100, "true"}, {100, "false"}, {101, "true"}, {101, "false"}}
	rows, err := pool.Query(ctx, `SELECT channel_id, payload->>'connected' FROM outbox WHERE type = $1 ORDER BY id`, bus.VoiceState)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var ch int64
		var conn string
		if err := rows.Scan(&ch, &conn); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || ch != want[i].ch || conn != want[i].conn {
			t.Fatalf("event %d = %d %s, want %+v", i, ch, conn, want)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("got %d events, want %d", i, len(want))
	}
}

func TestTextChannelAndUnknownChannel(t *testing.T) {
	svc, _ := setup(t)
	_, err := svc.Join(context.Background(), 1, 102)
	if he, ok := err.(*httpx.Error); !ok || he.Status != 422 {
		t.Fatalf("text channel: %v", err)
	}
	_, err = svc.Join(context.Background(), 1, 999)
	if he, ok := err.(*httpx.Error); !ok || he.Status != 404 {
		t.Fatalf("unknown channel: %v", err)
	}
}

func TestDisabledReturns501(t *testing.T) {
	pool := dbtest.New(t)
	svc := NewService(pool, ids.NewGenerator(1), fakeChannels{}, Config{})
	_, err := svc.Join(context.Background(), 1, 100)
	if he, ok := err.(*httpx.Error); !ok || he.Status != 501 || he.Code != "not_supported" {
		t.Fatalf("err = %v", err)
	}
}

// Joins concorrentes do mesmo usuário em canais diferentes terminam com uma linha só, e o saldo de eventos fecha.
func TestConcurrentJoinsLeaveOneRow(t *testing.T) {
	svc, pool := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Join(ctx, 1, 100+int64(i%2)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := count(t, pool, `SELECT count(*) FROM voice_states WHERE user_id = 1`); n != 1 {
		t.Fatalf("rows = %d", n)
	}
	in := count(t, pool, `SELECT count(*) FROM outbox WHERE type = $1 AND payload->>'connected' = 'true'`, bus.VoiceState)
	out := count(t, pool, `SELECT count(*) FROM outbox WHERE type = $1 AND payload->>'connected' = 'false'`, bus.VoiceState)
	if in-out != 1 {
		t.Fatalf("connected=%d disconnected=%d", in, out)
	}
}

func TestSweepRemovesStaleAndNonMembers(t *testing.T) {
	svc, pool := setup(t)
	ctx := context.Background()
	for _, u := range []int64{1, 2} {
		if _, err := svc.Join(ctx, u, 100); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := svc.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("fresh sweep = %d %v", n, err)
	}
	// 2 perde o heartbeat; 1 deixa de ser membro sem que o member.leave tenha chegado
	if _, err := pool.Exec(ctx, `UPDATE voice_states SET updated_at = now() - interval '1 hour' WHERE user_id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM members WHERE guild_id = 10 AND user_id = 1`); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Sweep(ctx); err != nil || n != 2 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM voice_states`); n != 0 {
		t.Fatalf("rows left = %d", n)
	}
}

func TestSnapshotAndSetState(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	if _, err := svc.Join(ctx, 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetState(ctx, 1, true, true, false); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.Snapshot(ctx, 2) // 2 é membro da guild
	if err != nil || len(snap) != 1 {
		t.Fatalf("snapshot = %v %v", snap, err)
	}
	if out, _ := svc.Snapshot(ctx, 3); len(out) != 0 { // 3 não é membro de nada
		t.Fatalf("outsider snapshot = %v", out)
	}
	ps, _ := svc.Participants(ctx, 2, 100)
	if len(ps) != 1 || !ps[0].Muted || !ps[0].CamOn || ps[0].Sharing {
		t.Fatalf("participants = %+v", ps)
	}
	// sem estado de voz, SetState não faz nada
	if err := svc.SetState(ctx, 2, true, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutSpeakJoinsMutedAndCannotUnmute(t *testing.T) {
	svc, _ := setup(t)
	svc.ch = fakeChannels{speak: false}
	ctx := context.Background()
	if _, err := svc.Join(ctx, 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetState(ctx, 1, false, false, false); err != nil {
		t.Fatal(err)
	}
	ps, _ := svc.Participants(ctx, 1, 100)
	if len(ps) != 1 || !ps[0].Muted {
		t.Fatalf("must stay muted: %+v", ps)
	}
}
