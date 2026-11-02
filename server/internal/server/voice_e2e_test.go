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
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
)

var voiceSecret = []byte("segredo-compartilhado-0123456789")

type voiceEnv struct {
	srv  *httptest.Server
	app  *App
	pool *pgxpool.Pool
}

// newVoiceServer sobe a app inteira (relay, gateway, jobs, voz) com a voz ligada ou não.
func newVoiceServer(t *testing.T, voiceOn bool) voiceEnv {
	t.Helper()
	cfg := config.Config{ServerName: "Teste", Registration: "open", JWTSecret: []byte(strings.Repeat("k", 32))}
	if voiceOn {
		cfg.Voice = config.Voice{Secret: voiceSecret, SFUURL: "wss://sfu.example/v1/media"}
	}
	pool := dbtest.New(t)
	app := NewApp(cfg, pool)
	srv := httptest.NewServer(app.Handler)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); app.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait(); srv.Close() })
	return voiceEnv{srv, app, pool}
}

// sfuAccepts valida o token como o SFU (experimentos/sfu/sfu.go) e devolve as claims.
func sfuAccepts(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	c := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, c, func(*jwt.Token) (any, error) { return voiceSecret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kurz-manager"), jwt.WithExpirationRequired())
	if err != nil {
		t.Fatalf("SFU would reject the token: %v", err)
	}
	return c
}

func participantIDs(v map[string]any) []string {
	var out []string
	for _, p := range v["participants"].([]any) {
		out = append(out, p.(map[string]any)["user"].(map[string]any)["id"].(string))
	}
	return out
}

func (c *wsClient) send(op string, d any) {
	c.t.Helper()
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// eventOf pula os dispatches de outros tipos até achar t.
func (c *wsClient) eventOf(t string) map[string]any {
	c.t.Helper()
	for {
		f := c.expect("dispatch")
		if f.T != t {
			continue
		}
		var d map[string]any
		if err := json.Unmarshal(f.D, &d); err != nil {
			c.t.Fatal(err)
		}
		return d
	}
}

func voiceState(d map[string]any) (channel string, connected bool, user string) {
	p := d["participant"].(map[string]any)["user"].(map[string]any)
	return d["channelId"].(string), d["connected"].(bool), p["id"].(string)
}

func mkChannel(u *user, gid, name, kind string) string {
	return u.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": name, "kind": kind})["id"].(string)
}

func TestVoiceJoinPermissionsAndToken(t *testing.T) {
	env := newVoiceServer(t, true)
	srv := env.srv.URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	room := mkChannel(alice, gid, "sala", "voice")
	text := mkChannel(alice, gid, "papo", "text")

	// canal de texto: 422
	bob.fail("POST", "/v1/channels/"+text+"/voice/join", 422, "validation", nil)
	bob.fail("GET", "/v1/channels/"+text+"/voice", 422, "validation", nil)

	// join devolve o VoiceSession; o token passa na validação do SFU
	s := alice.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	media := s["media"].(map[string]any)
	if media["url"] != "wss://sfu.example/v1/media" {
		t.Fatalf("media.url = %v", media["url"])
	}
	c := sfuAccepts(t, media["token"].(string))
	if c["sub"] != alice.id || c["room"] != room || c["name"] == "" || c["speak"] != true {
		t.Fatalf("claims = %v", c)
	}
	if exp := time.Unix(int64(c["exp"].(float64)), 0); time.Until(exp) > 61*time.Second || time.Until(exp) < 55*time.Second {
		t.Fatalf("exp = %v", exp)
	}
	if ice, _ := s["iceServers"].([]any); len(ice) == 0 {
		t.Fatalf("iceServers = %v", s["iceServers"])
	}
	if got := participantIDs(s); len(got) != 1 || got[0] != alice.id {
		t.Fatalf("participants = %v", got)
	}
	// cada join emite um token novo (jti diferente)
	again := alice.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	if sfuAccepts(t, again["media"].(map[string]any)["token"].(string))["jti"] == c["jti"] {
		t.Fatal("jti must differ between tokens")
	}

	// sem Connect: 403 missing_permission e nenhum token
	alice.do("PUT", "/v1/channels/"+room+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.Connect)})
	bob.fail("POST", "/v1/channels/"+room+"/voice/join", 403, "missing_permission", nil)

	// canal que não vê: 404
	alice.do("PUT", "/v1/channels/"+room+"/overrides/member/"+carol.id, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	carol.fail("POST", "/v1/channels/"+room+"/voice/join", 404, "not_found", nil)
	carol.fail("GET", "/v1/channels/"+room+"/voice", 404, "not_found", nil)

	// só com Connect (sem Speak): entra, mas mutado, e o token diz speak=false
	alice.do("PUT", "/v1/channels/"+room+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.Speak)})
	bs := bob.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	if sfuAccepts(t, bs["media"].(map[string]any)["token"].(string))["speak"] != false {
		t.Fatal("token must carry speak=false")
	}
	for _, p := range bs["participants"].([]any) {
		m := p.(map[string]any)
		if m["user"].(map[string]any)["id"] == bob.id && m["muted"] != true {
			t.Fatalf("bob without Speak must be muted: %v", m)
		}
	}

	// sem token: 401
	if status, _ := call(t, "POST", srv+"/v1/channels/"+room+"/voice/join", "", nil); status != 401 {
		t.Fatalf("anonymous join = %d", status)
	}
}

func TestVoiceMoveLeaveAndList(t *testing.T) {
	env := newVoiceServer(t, true)
	srv := env.srv.URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	one, two := mkChannel(alice, gid, "um", "voice"), mkChannel(alice, gid, "dois", "voice")
	list := func(ch string) []string {
		return participantIDs(bob.do("GET", "/v1/channels/"+ch+"/voice", 200, nil))
	}

	alice.do("POST", "/v1/channels/"+one+"/voice/join", 200, nil)
	bob.do("POST", "/v1/channels/"+one+"/voice/join", 200, nil)
	if got := list(one); len(got) != 2 {
		t.Fatalf("one = %v", got)
	}
	// entrar em outro canal move: sai do primeiro
	alice.do("POST", "/v1/channels/"+two+"/voice/join", 200, nil)
	if got := list(one); len(got) != 1 || got[0] != bob.id {
		t.Fatalf("one after move = %v", got)
	}
	if got := list(two); len(got) != 1 || got[0] != alice.id {
		t.Fatalf("two after move = %v", got)
	}
	// leave é idempotente e só vale para quem está no canal
	bob.do("POST", "/v1/channels/"+two+"/voice/leave", 204, nil)
	if got := list(one); len(got) != 1 {
		t.Fatalf("leave of another channel must not remove: %v", got)
	}
	bob.do("POST", "/v1/channels/"+one+"/voice/leave", 204, nil)
	bob.do("POST", "/v1/channels/"+one+"/voice/leave", 204, nil)
	if got := list(one); len(got) != 0 {
		t.Fatalf("one after leave = %v", got)
	}
}

func TestVoiceStateEventsAreFilteredByViewChannel(t *testing.T) {
	env := newVoiceServer(t, true)
	srv := env.srv.URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	open := mkChannel(alice, gid, "aberta", "voice")
	secret := mkChannel(alice, gid, "secreta", "voice")
	alice.do("PUT", "/v1/channels/"+secret+"/overrides/role/"+gid, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	alice.do("PUT", "/v1/channels/"+secret+"/overrides/member/"+bob.id, 200, map[string]any{"allow": perm(permissions.ViewChannel)})

	cb, cc := connect(t, env.srv, bob), connect(t, env.srv, carol)
	_ = cc

	// alice entra no canal secreta: bob vê, carol não
	alice.do("POST", "/v1/channels/"+secret+"/voice/join", 200, nil)
	ch, connected, uid := voiceState(cb.eventOf("voice.state"))
	if ch != secret || !connected || uid != alice.id {
		t.Fatalf("bob got %s %v %s", ch, connected, uid)
	}
	// move para a aberta: bob recebe saída da secreta e entrada na aberta; carol só a entrada
	alice.do("POST", "/v1/channels/"+open+"/voice/join", 200, nil)
	if ch, connected, _ := voiceState(cb.eventOf("voice.state")); ch != secret || connected {
		t.Fatalf("bob expected leave of secret, got %s %v", ch, connected)
	}
	if ch, connected, _ := voiceState(cb.eventOf("voice.state")); ch != open || !connected {
		t.Fatalf("bob expected join of open, got %s %v", ch, connected)
	}
	ch, connected, uid = voiceState(cc.eventOf("voice.state"))
	if ch != open || !connected || uid != alice.id {
		t.Fatalf("carol first voice.state must be the open join, got %s %v %s", ch, connected, uid)
	}

	// o ready de quem conecta agora traz o estado atual, só dos canais que vê
	alice.do("POST", "/v1/channels/"+secret+"/voice/join", 200, nil) // alice volta para a secreta
	bob.do("POST", "/v1/channels/"+open+"/voice/join", 200, nil)
	var rb, rc struct {
		Voice []map[string]any `json:"voice"`
	}
	late := connect(t, env.srv, carol)
	_ = json.Unmarshal(late.ready.D, &rc)
	if len(rc.Voice) != 1 || rc.Voice[0]["channelId"] != open {
		t.Fatalf("carol ready.voice = %v", rc.Voice)
	}
	lateBob := connect(t, env.srv, bob)
	_ = json.Unmarshal(lateBob.ready.D, &rb)
	if len(rb.Voice) != 2 {
		t.Fatalf("bob ready.voice = %v", rb.Voice)
	}

	// voice.state.set: mute chega a quem vê o canal
	cc2 := connect(t, env.srv, carol)
	bob.do("POST", "/v1/channels/"+open+"/voice/join", 200, nil)
	lateBob.send("voice.state.set", map[string]bool{"muted": true, "camOn": true, "sharing": false})
	for {
		d := cc2.eventOf("voice.state")
		_, _, uid := voiceState(d)
		p := d["participant"].(map[string]any)
		if uid == bob.id && p["muted"] == true {
			if p["camOn"] != true || p["sharing"] != false {
				t.Fatalf("participant = %v", p)
			}
			break
		}
	}
}


func TestVoiceStateRemovedOnKickBanAndLeave(t *testing.T) {
	env := newVoiceServer(t, true)
	srv := env.srv.URL
	alice, bob, carol, dave := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol"), newUser(t, srv, "dave")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	for _, u := range []*user{bob, carol, dave} {
		u.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	}
	room := mkChannel(alice, gid, "sala", "voice")
	watcher := connect(t, env.srv, alice)
	for _, u := range []*user{bob, carol, dave} {
		u.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
		if _, c, id := voiceState(watcher.eventOf("voice.state")); !c || id != u.id {
			t.Fatalf("join event for %s", u.id)
		}
	}
	expectGone := func(who *user) {
		t.Helper()
		ch, connected, id := voiceState(watcher.eventOf("voice.state"))
		if ch != room || connected || id != who.id {
			t.Fatalf("expected leave of %s, got %s %v %s", who.id, ch, connected, id)
		}
	}
	alice.do("DELETE", "/v1/guilds/"+gid+"/members/"+bob.id, 204, nil) // kick
	expectGone(bob)
	alice.do("PUT", "/v1/guilds/"+gid+"/bans/"+carol.id, 204, map[string]any{"reason": "x"}) // ban
	expectGone(carol)
	dave.do("DELETE", "/v1/guilds/"+gid+"/members/@me", 204, nil) // sair da guild
	expectGone(dave)
	alice.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	if got := participantIDs(alice.do("GET", "/v1/channels/"+room+"/voice", 200, nil)); len(got) != 1 || got[0] != alice.id {
		t.Fatalf("participants = %v", got)
	}
	// banido não entra de novo (já não é membro: 404)
	carol.fail("POST", "/v1/channels/"+room+"/voice/join", 404, "not_found", nil)
}

func TestVoiceHeartbeatKeepsAndSweepRemoves(t *testing.T) {
	env := newVoiceServer(t, true)
	srv := env.srv.URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	room := mkChannel(alice, gid, "sala", "voice")
	alice.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	bob.do("POST", "/v1/channels/"+room+"/voice/join", 200, nil)
	ctx := context.Background()
	age := func(u *user) {
		if _, err := env.pool.Exec(ctx, `UPDATE voice_states SET updated_at = now() - interval '1 hour' WHERE user_id = $1`, u.id); err != nil {
			t.Fatal(err)
		}
	}
	age(alice)
	age(bob)

	// heartbeat do gateway renova o alice; o bob (sem heartbeat) é varrido
	ca := connect(t, env.srv, alice)
	ca.send("heartbeat", nil)
	ca.expect("heartbeat_ack")
	watcher := connect(t, env.srv, alice)
	n, err := env.app.voice.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	if _, connected, id := voiceState(watcher.eventOf("voice.state")); connected || id != bob.id {
		t.Fatalf("expected bob to be swept, got %v %s", connected, id)
	}
	if got := participantIDs(alice.do("GET", "/v1/channels/"+room+"/voice", 200, nil)); len(got) != 1 || got[0] != alice.id {
		t.Fatalf("participants = %v", got)
	}
}

func TestVoiceNotConfigured(t *testing.T) {
	env := newVoiceServer(t, false)
	srv := env.srv.URL
	alice := newUser(t, srv, "alice")
	gid := createGuild(alice, "Casa")
	room := mkChannel(alice, gid, "sala", "voice")
	alice.fail("POST", "/v1/channels/"+room+"/voice/join", 501, "not_supported", nil)
	alice.fail("GET", "/v1/channels/"+room+"/voice", 501, "not_supported", nil)
	_, body := call(t, "GET", srv+"/.well-known/kurz", "", nil)
	for _, c := range body["capabilities"].([]any) {
		if c == "voice" {
			t.Fatalf("voice must not be advertised: %v", body["capabilities"])
		}
	}
	// o ready não traz voice
	c := connect(t, env.srv, alice)
	if strings.Contains(string(c.ready.D), `"voice"`) {
		t.Fatalf("ready = %s", c.ready.D)
	}
}

func TestVoiceAdvertisedWhenConfigured(t *testing.T) {
	env := newVoiceServer(t, true)
	_, body := call(t, "GET", env.srv.URL+"/.well-known/kurz", "", nil)
	found := false
	for _, c := range body["capabilities"].([]any) {
		found = found || c == "voice"
	}
	if !found || body["contract"] != "0.6" {
		t.Fatalf("discovery = %v", body)
	}
}
