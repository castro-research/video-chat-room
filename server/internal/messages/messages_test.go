package messages

import (
	"context"
	"strings"
	"sync"
	"testing"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

func TestValidText(t *testing.T) {
	for text, ok := range map[string]bool{
		"": false, "  \n\t": false, "oi": true,
		strings.Repeat("ã", MaxTextRunes):   true,
		strings.Repeat("a", MaxTextRunes+1): false,
	} {
		if got := validText(text) == nil; got != ok {
			t.Errorf("validText(%.10q...) = %v, queria %v", text, got, ok)
		}
	}
}

func TestValidEmoji(t *testing.T) {
	for emoji, ok := range map[string]bool{
		"": false, "👍": true, "👨‍👩‍👧": true, "a b": false, "a\nb": false, "tada": true,
		strings.Repeat("x", MaxEmojiBytes+1): false, "\xff": false,
	} {
		if got := validEmoji(emoji) == nil; got != ok {
			t.Errorf("validEmoji(%q) = %v, queria %v", emoji, got, ok)
		}
	}
}

// fakePerms dá permissões fixas por usuário num canal de texto, sem tocar nos outros módulos.
type fakePerms map[int64]permissions.Perm

func (f fakePerms) ChannelAccess(_ context.Context, user, _ int64) (Access, error) {
	p, ok := f[user]
	if !ok {
		return Access{}, httpx.ErrNotFound
	}
	return Access{Perms: p, Text: true}, nil
}

func (f fakePerms) VisibleTextChannels(context.Context, int64, int64) ([]int64, error) {
	return nil, nil
}

func setup(t *testing.T, perms fakePerms) (*Service, int64) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.New(t)
	const gid, cid = 500, 501
	for u := range perms {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, name, password_hash) VALUES ($1, $2, $2, 'x')`, u, "u"+ids.Format(u)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO guilds (id, name, owner_id) VALUES ($1, 'g', 1)`, gid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO channels (id, guild_id, kind, name, position) VALUES ($1, $2, 'text', 'c', 0)`, cid, gid); err != nil {
		t.Fatal(err)
	}
	return NewService(pool, ids.NewGenerator(1), perms), cid
}

func TestStoreRoundTripAndConstraints(t *testing.T) {
	ctx := context.Background()
	const alice, bob = 1, 2
	svc, ch := setup(t, fakePerms{alice: permissions.DefaultEveryone, bob: permissions.DefaultEveryone})

	m, err := svc.Send(ctx, alice, ch, SendInput{Text: "oi", Nonce: "n"})
	if err != nil || m.Nonce != "n" || m.Author.Username != "u1" || m.EditedAt != nil {
		t.Fatalf("send = %+v, %v", m, err)
	}
	reply, err := svc.Send(ctx, bob, ch, SendInput{Text: "re", ReplyToID: &m.ID})
	if err != nil || reply.ReplyToID == nil || *reply.ReplyToID != m.ID {
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	// apagar o original zera o ponteiro da resposta (FK SET NULL)
	if err := svc.Delete(ctx, alice, ch, m.ID); err != nil {
		t.Fatal(err)
	}
	page, err := svc.List(ctx, bob, ch, ListInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ReplyToID != nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	// o CHECK do banco segura texto vazio mesmo se o serviço falhar
	if _, err := svc.pool.Exec(ctx, `INSERT INTO messages (id, channel_id, author_id, content) VALUES (9, $1, 1, '')`, ch); err == nil {
		t.Fatal("empty content must violate the CHECK")
	}
	// reações caem junto com a mensagem
	if err := svc.AddReaction(ctx, alice, ch, reply.ID, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, bob, ch, reply.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := svc.pool.QueryRow(ctx, `SELECT count(*) FROM reactions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("reactions left = %d, %v", n, err)
	}
}

func TestRecordHookRunsInsideWriteTransaction(t *testing.T) {
	// Garante que cada escrita passa por record: o Plano 4 troca o corpo por um INSERT no outbox.
	ctx := context.Background()
	svc, ch := setup(t, fakePerms{1: permissions.DefaultEveryone | permissions.ManageMessages})
	var kinds []string
	svc.onRecord = func(e Event) { kinds = append(kinds, e.Kind) }
	m, _ := svc.Send(ctx, 1, ch, SendInput{Text: "a"})
	_, _ = svc.Edit(ctx, 1, ch, m.ID, "b")
	_ = svc.AddReaction(ctx, 1, ch, m.ID, "x")
	_ = svc.RemoveReaction(ctx, 1, ch, m.ID, "x", 0)
	_ = svc.Delete(ctx, 1, ch, m.ID)
	want := "message.create message.update reaction.add reaction.remove message.delete"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events = %q", got)
	}
}

func TestConcurrentSendsKeepIDOrderEqualToCommitOrder(t *testing.T) {
	ctx := context.Background()
	const users, per = 8, 15
	perms := fakePerms{}
	for u := int64(1); u <= users; u++ {
		perms[u] = permissions.DefaultEveryone
	}
	svc, ch := setup(t, perms)
	var wg sync.WaitGroup
	for u := int64(1); u <= users; u++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range per {
				if _, err := svc.Send(ctx, u, ch, SendInput{Text: "x"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	// a ordem física de commit (xmin) tem de bater com a ordem dos ids
	rows, err := svc.pool.Query(ctx, `SELECT id FROM messages ORDER BY xmin::text::bigint, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var prev int64
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if id <= prev {
			t.Fatalf("id %d after %d", id, prev)
		}
		prev, n = id, n+1
	}
	if n != users*per {
		t.Fatalf("%d messages", n)
	}
}

// Cada escrita grava o evento no outbox na mesma transação: escrita que falha não deixa evento.
func TestWritesEnqueueOutboxEvents(t *testing.T) {
	ctx := context.Background()
	svc, ch := setup(t, fakePerms{1: permissions.DefaultEveryone, 2: permissions.DefaultEveryone})
	m, err := svc.Send(ctx, 1, ch, SendInput{Text: "a", Nonce: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Edit(ctx, 1, ch, m.ID, "b"); err != nil {
		t.Fatal(err)
	}
	_ = svc.AddReaction(ctx, 2, ch, m.ID, "x")
	_ = svc.RemoveReaction(ctx, 2, ch, m.ID, "x", 0)
	_ = svc.Delete(ctx, 1, ch, m.ID)
	// falhas de validação/permissão não deixam rastro
	_, _ = svc.Send(ctx, 1, ch, SendInput{Text: ""})
	_ = svc.Delete(ctx, 2, ch, m.ID)

	rows, err := svc.pool.Query(ctx, `SELECT type, guild_id, channel_id, payload FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var typ string
		var g, c int64
		var payload map[string]any
		if err := rows.Scan(&typ, &g, &c, &payload); err != nil {
			t.Fatal(err)
		}
		if g != 500 || c != ch {
			t.Fatalf("%s: guild %d channel %d", typ, g, c)
		}
		if typ == "message.create" && (payload["nonce"] != "n1" || payload["text"] != "a") {
			t.Fatalf("create payload = %v", payload)
		}
		kinds = append(kinds, typ)
	}
	if got, want := strings.Join(kinds, " "), "message.create message.update reaction.add reaction.remove message.delete"; got != want {
		t.Fatalf("outbox = %q", got)
	}
}
