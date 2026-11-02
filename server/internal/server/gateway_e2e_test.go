package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
)

// newRealtimeServer sobe a app inteira com o relay e o gateway rodando, como o binário.
func newRealtimeServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{ServerName: "Teste", Registration: "open", JWTSecret: []byte(strings.Repeat("k", 32))}
	app := NewApp(cfg, dbtest.New(t))
	srv := httptest.NewServer(app.Handler)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); app.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait(); srv.Close() })
	return srv
}

type wsFrame struct {
	Op  string          `json:"op"`
	T   string          `json:"t"`
	Seq uint64          `json:"seq"`
	D   json.RawMessage `json:"d"`
}

type wsClient struct {
	t      *testing.T
	ws     *websocket.Conn
	frames chan wsFrame
	ready  wsFrame // o frame ready desta conexão
	errc   chan error
}

func connect(t *testing.T, srv *httptest.Server, u *user) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/gateway", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := &wsClient{t: t, ws: ws, frames: make(chan wsFrame, 1024), errc: make(chan error, 1)}
	t.Cleanup(func() { _ = ws.CloseNow() })
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				c.errc <- err
				return
			}
			var f wsFrame
			_ = json.Unmarshal(data, &f)
			c.frames <- f
		}
	}()
	c.expect("hello")
	b, _ := json.Marshal(map[string]any{"op": "identify", "d": map[string]string{"token": u.token}})
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	c.ready = c.expect("ready")
	return c
}

func (c *wsClient) next() wsFrame {
	c.t.Helper()
	select {
	case f := <-c.frames:
		return f
	case err := <-c.errc:
		c.t.Fatalf("connection closed: %v", err)
	case <-time.After(10 * time.Second):
		c.t.Fatal("timed out waiting for a frame")
	}
	return wsFrame{}
}

func (c *wsClient) expect(op string) wsFrame {
	c.t.Helper()
	f := c.next()
	if f.Op != op {
		c.t.Fatalf("got %s %s %s, want %s", f.Op, f.T, f.D, op)
	}
	return f
}

// event espera um dispatch do tipo t e devolve o d como mapa.
func (c *wsClient) event(t string) map[string]any {
	c.t.Helper()
	f := c.expect("dispatch")
	if f.T != t {
		c.t.Fatalf("got event %s %s, want %s", f.T, f.D, t)
	}
	var d map[string]any
	if err := json.Unmarshal(f.D, &d); err != nil {
		c.t.Fatal(err)
	}
	return d
}

func (c *wsClient) quiet() {
	c.t.Helper()
	select {
	case f := <-c.frames:
		c.t.Fatalf("unexpected frame %s %s %s", f.Op, f.T, f.D)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestGatewayRESTToWebSocket(t *testing.T) {
	srv := newRealtimeServer(t)
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	// conectam antes de a guild existir: os eventos de montagem chegam em ordem e o teste os consome,
	// sem corrida entre o ready e eventos que já estavam a caminho
	ca, cb := connect(t, srv, alice), connect(t, srv, bob)
	gid := createGuild(alice, "Casa")
	ca.event("guild.create")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	ca.event("member.join")
	cb.event("member.join")
	ch := channelNamed(alice, gid, "geral")["id"].(string)

	start := time.Now()
	m := alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "oi", "nonce": "n-1"})
	got := cb.event("message.create")
	t.Logf("REST POST -> WS message.create: %v", time.Since(start))
	if got["id"] != m["id"] || got["text"] != "oi" || got["channelId"] != ch {
		t.Fatalf("bob got %v", got)
	}
	if _, has := got["nonce"]; has {
		t.Fatalf("nonce leaked to another user: %v", got)
	}
	if mine := ca.event("message.create"); mine["nonce"] != "n-1" || mine["id"] != m["id"] {
		t.Fatalf("alice (sender) got %v", mine)
	}

	// editar, reagir, apagar
	bobMsg := bob.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "oi alice"})
	ca.event("message.create")
	cb.event("message.create")
	bob.do("PATCH", "/v1/channels/"+ch+"/messages/"+bobMsg["id"].(string), 200, map[string]any{"text": "editado"})
	if u := ca.event("message.update"); u["text"] != "editado" || u["id"] != bobMsg["id"] {
		t.Fatalf("update = %v", u)
	}
	cb.event("message.update")
	alice.do("PUT", "/v1/channels/"+ch+"/messages/"+bobMsg["id"].(string)+"/reactions/👍/@me", 204, nil)
	if r := cb.event("reaction.add"); r["emoji"] != "👍" || r["userId"] != alice.id {
		t.Fatalf("reaction = %v", r)
	}
	ca.event("reaction.add")
	bob.do("DELETE", "/v1/channels/"+ch+"/messages/"+bobMsg["id"].(string), 204, nil)
	if d := ca.event("message.delete"); d["id"] != bobMsg["id"] {
		t.Fatalf("delete = %v", d)
	}
	cb.event("message.delete")
}

func TestGatewayPermissionFilteringE2E(t *testing.T) {
	srv := newRealtimeServer(t)
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	ca, cb := connect(t, srv, alice), connect(t, srv, bob)
	gid := createGuild(alice, "Casa")
	ca.event("guild.create")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	ca.event("member.join")
	cb.event("member.join")
	gbase := "/v1/guilds/" + gid

	// cada passo espera o evento dos dois antes da próxima mudança, para o roteamento ver o estado daquele passo
	secret := alice.do("POST", gbase+"/channels", 201, map[string]any{"name": "secreto", "kind": "text"})["id"].(string)
	ca.event("channel.create")
	cb.event("channel.create")
	ov := "/v1/channels/" + secret + "/overrides/role/" + gid
	alice.do("PUT", ov, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	ca.event("channel.permissions")
	cb.quiet() // bob acabou de perder a visão do canal
	geral := channelNamed(alice, gid, "geral")["id"].(string)
	say := func(ch, text string) {
		alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": text})
	}

	say(secret, "só para quem pode")
	say(geral, "para todos")
	if m := ca.event("message.create"); m["text"] != "só para quem pode" {
		t.Fatalf("alice got %v", m)
	}
	ca.event("message.create")
	if m := cb.event("message.create"); m["text"] != "para todos" { // a do canal secreto não chegou
		t.Fatalf("bob got %v", m)
	}

	// libera: a próxima mensagem do canal já chega ao bob (cache invalidado pelo evento do override)
	alice.do("PUT", ov, 200, map[string]any{"allow": perm(permissions.ViewChannel)})
	ca.event("channel.permissions")
	cb.event("channel.permissions")
	say(secret, "agora pode")
	ca.event("message.create")
	if m := cb.event("message.create"); m["text"] != "agora pode" {
		t.Fatalf("bob got %v", m)
	}

	// revoga de novo
	alice.do("PUT", ov, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	ca.event("channel.permissions")
	say(secret, "de novo escondida")
	ca.event("message.create")
	cb.quiet()
}

func TestGatewayGuildChannelAndMemberEvents(t *testing.T) {
	srv := newRealtimeServer(t)
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	ca, cb := connect(t, srv, alice), connect(t, srv, bob)

	gid := createGuild(alice, "Casa") // alice passa a receber a guild
	if g := ca.event("guild.create"); g["userId"] != alice.id || g["guild"].(map[string]any)["id"] != gid {
		t.Fatalf("guild.create = %v", g)
	}
	cb.quiet()
	gbase := "/v1/guilds/" + gid

	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	if j := ca.event("member.join"); j["userId"] != bob.id {
		t.Fatalf("member.join = %v", j)
	}
	cb.event("member.join") // o próprio bob já recebe a guild

	ch := alice.do("POST", gbase+"/channels", 201, map[string]any{"name": "novo", "kind": "text"})
	ca.event("channel.create")
	if c := cb.event("channel.create"); c["name"] != "novo" {
		t.Fatalf("channel.create = %v", c)
	}
	alice.do("PATCH", "/v1/channels/"+ch["id"].(string), 200, map[string]any{"name": "renomeado"})
	ca.event("channel.update")
	if c := cb.event("channel.update"); c["name"] != "renomeado" {
		t.Fatalf("channel.update = %v", c)
	}

	role := alice.do("POST", gbase+"/roles", 201, map[string]any{"name": "vip"})["id"].(string)
	ca.event("role.create")
	cb.event("role.create")
	alice.do("PUT", gbase+"/members/"+bob.id+"/roles/"+role, 204, nil)
	if u := cb.event("member.update"); u["userId"] != bob.id {
		t.Fatalf("member.update = %v", u)
	}
	ca.event("member.update")
	alice.do("DELETE", gbase+"/roles/"+role, 204, nil)
	ca.event("role.delete")
	cb.event("role.delete")

	alice.do("DELETE", "/v1/channels/"+ch["id"].(string), 204, nil)
	ca.event("channel.delete")
	cb.event("channel.delete")

	// expulso: recebe o member.leave e nada depois
	alice.do("DELETE", gbase+"/members/"+bob.id, 204, nil)
	if l := cb.event("member.leave"); l["reason"] != "kick" || l["userId"] != bob.id {
		t.Fatalf("member.leave = %v", l)
	}
	ca.event("member.leave")
	alice.do("POST", gbase+"/roles", 201, map[string]any{"name": "depois"})
	ca.event("role.create")
	cb.quiet()

	alice.do("DELETE", gbase, 204, nil)
	ca.event("guild.delete")
}

// O outbox é transacional: escrita recusada não deixa evento.
func TestOutboxHasNoEventForRejectedWrites(t *testing.T) {
	cfg := config.Config{ServerName: "Teste", Registration: "open", JWTSecret: []byte(strings.Repeat("k", 32))}
	pool := dbtest.New(t)
	srv := httptest.NewServer(New(cfg, pool))
	defer srv.Close()
	alice, bob := newUser(t, srv.URL, "alice"), newUser(t, srv.URL, "bob")
	gid := createGuild(alice, "Casa")
	count := func() (n int) {
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	before := count()
	bob.fail("POST", "/v1/guilds/"+gid+"/channels", 404, "not_found", map[string]any{"name": "x", "kind": "text"})
	alice.fail("POST", "/v1/guilds/"+gid+"/channels", 422, "validation", map[string]any{"name": "", "kind": "text"})
	if count() != before {
		t.Fatalf("rejected writes left %d outbox rows", count()-before)
	}
}
