package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/platform/db"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/ids"
)

const wait = 10 * time.Second

// ---- fakes ----

// fakeDir é o Directory dos testes: guilds por usuário e visibilidade por (usuário, canal), mutáveis.
type fakeDir struct {
	mu     sync.Mutex
	guilds map[int64][]Guild
	view   map[[2]int64]bool
	calls  atomic.Int64
}

func newDir() *fakeDir {
	return &fakeDir{guilds: map[int64][]Guild{}, view: map[[2]int64]bool{}}
}

func (d *fakeDir) Guilds(_ context.Context, u int64) ([]Guild, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.guilds[u], nil
}

func (d *fakeDir) CanView(_ context.Context, u, c int64) (bool, error) {
	d.calls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.view[[2]int64{u, c}], nil
}

func (d *fakeDir) setView(u, c int64, ok bool) {
	d.mu.Lock()
	d.view[[2]int64{u, c}] = ok
	d.mu.Unlock()
}

func fakeAuth(token string) (int64, error) {
	n, ok := strings.CutPrefix(token, "tok-")
	if !ok {
		return 0, errors.New("bad token")
	}
	return strconv.ParseInt(n, 10, 64)
}

// ---- ambiente ----

type env struct {
	pool *pgxpool.Pool
	bus  *bus.Postgres
	gw   *Gateway
	srv  *httptest.Server
	dir  *fakeDir
	gen  *ids.Generator
}

func (e *env) url() string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/v1/gateway" }

// newEnv sobe bus + gateway sobre pool. Vários envs no mesmo pool/banco simulam réplicas.
func newEnv(t *testing.T, pool *pgxpool.Pool, node int64, cfg Config, dir *fakeDir) *env {
	t.Helper()
	b := bus.NewPostgres(pool, ids.NewGenerator(node), bus.Options{Sweep: 100 * time.Millisecond})
	gw := New(cfg, AuthFunc(fakeAuth), dir, b)
	mux := http.NewServeMux()
	gw.Mount(mux)
	srv := httptest.NewServer(mux)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); b.Run(ctx) }()
	go func() { defer wg.Done(); gw.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait(); srv.Close() })
	return &env{pool: pool, bus: b, gw: gw, srv: srv, dir: dir, gen: ids.NewGenerator(node)}
}

// listening espera n listeners do bus conectados, para não publicar antes do LISTEN.
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
	t.Fatal("bus listener never connected")
}

func (e *env) publish(t *testing.T, typ string, guild, channel int64, payload any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	if err := e.bus.Publish(context.Background(), bus.Event{Type: typ, GuildID: guild, ChannelID: channel, Payload: raw}); err != nil {
		t.Fatal(err)
	}
}

// ---- cliente ----

type client struct {
	t      *testing.T
	ws     *websocket.Conn
	frames chan frame
	errc   chan error
}

func dialRaw(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ws.SetReadLimit(-1)
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

func dial(t *testing.T, url string) *client {
	t.Helper()
	c := &client{t: t, ws: dialRaw(t, url), frames: make(chan frame, 8192), errc: make(chan error, 1)}
	go func() {
		for {
			_, data, err := c.ws.Read(context.Background())
			if err != nil {
				c.errc <- err
				return
			}
			var f frame
			if err := json.Unmarshal(data, &f); err != nil {
				c.errc <- err
				return
			}
			c.frames <- f
		}
	}()
	return c
}

func (c *client) send(op string, d any) {
	c.t.Helper()
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatalf("send %s: %v", op, err)
	}
}

func (c *client) next() frame {
	c.t.Helper()
	select {
	case f := <-c.frames:
		return f
	case err := <-c.errc:
		c.t.Fatalf("connection closed while waiting for a frame: %v", err)
	case <-time.After(wait):
		c.t.Fatal("timed out waiting for a frame")
	}
	return frame{}
}

func (c *client) expect(op string) frame {
	c.t.Helper()
	f := c.next()
	if f.Op != op {
		c.t.Fatalf("got op %q (%s), want %q", f.Op, f.D, op)
	}
	return f
}

// quiet falha se algum frame chegar nos próximos instantes.
func (c *client) quiet() {
	c.t.Helper()
	select {
	case f := <-c.frames:
		c.t.Fatalf("unexpected frame %s %s %s", f.Op, f.T, f.D)
	case <-time.After(300 * time.Millisecond):
	}
}

func (c *client) closeCode() websocket.StatusCode {
	c.t.Helper()
	select {
	case err := <-c.errc:
		return websocket.CloseStatus(err)
	case <-time.After(wait):
		c.t.Fatal("connection did not close")
		return -1
	}
}

// open dá hello + identify + ready.
func open(t *testing.T, e *env, user int64) (*client, readyData) {
	t.Helper()
	c := dial(t, e.url())
	c.expect(opHello)
	c.send(opIdentify, identifyData{Token: fmt.Sprintf("tok-%d", user)})
	var rd readyData
	if err := json.Unmarshal(c.expect(opReady).D, &rd); err != nil {
		t.Fatal(err)
	}
	return c, rd
}

func (c *client) dispatch(typ string) frame {
	c.t.Helper()
	f := c.expect(opDispatch)
	if f.T != typ {
		c.t.Fatalf("got %s %s, want %s", f.T, f.D, typ)
	}
	return f
}

const (
	guildA   = int64(1000)
	channelX = int64(2000)
	alice    = int64(1)
	bob      = int64(2)
)

func standardDir() *fakeDir {
	d := newDir()
	d.guilds[alice] = []Guild{{ID: guildA, Name: "Casa", OwnerID: alice}}
	d.guilds[bob] = []Guild{{ID: guildA, Name: "Casa", OwnerID: alice}}
	d.setView(alice, channelX, true)
	d.setView(bob, channelX, true)
	return d
}

// ---- testes ----

func TestIdentifyRejectsBadToken(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{}, standardDir())
	c := dial(t, e.url())
	c.expect(opHello)
	c.send(opIdentify, identifyData{Token: "garbage"})
	if code := c.closeCode(); code != CloseAuthFailed {
		t.Fatalf("close code = %d", code)
	}

	c = dial(t, e.url())
	c.expect(opHello)
	c.send("dispatch", nil) // opcode que não é identify/resume
	if code := c.closeCode(); code != CloseBadFrame {
		t.Fatalf("close code = %d", code)
	}
}

func TestIdentifyTimeout(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{IdentifyTimeout: 150 * time.Millisecond}, standardDir())
	c := dial(t, e.url())
	c.expect(opHello)
	if code := c.closeCode(); code != CloseIdentifyTimeout {
		t.Fatalf("close code = %d", code)
	}
}

func TestReadyHasSessionAndGuilds(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{}, standardDir())
	c, rd := open(t, e, alice)
	if rd.SessionID == "" || rd.User.ID != "1" || len(rd.Guilds) != 1 || rd.Guilds[0].ID != "1000" || rd.Guilds[0].Name != "Casa" || rd.Guilds[0].OwnerID != "1" {
		t.Fatalf("ready = %+v", rd)
	}
	c.send(opHeartbeat, nil)
	c.expect(opHeartbeatAck)
}

func TestHeartbeatSilenceDropsConnection(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{HeartbeatInterval: 100 * time.Millisecond}, standardDir())
	c, _ := open(t, e, alice)
	// sem heartbeat por 2 intervalos o servidor derruba a conexão (a sessão segue viva para resume)
	if code := c.closeCode(); code != CloseHeartbeatTimeout {
		t.Fatalf("close code = %d", code)
	}
}

func TestMessageCreateReachesOthersWithNonceOnlyForAuthor(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{}, standardDir())
	listening(t, pool, 1)
	ca, _ := open(t, e, alice)
	cb, _ := open(t, e, bob)

	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "9", "text": "oi", "nonce": "n-1", "author": map[string]any{"id": "1"}})
	e.publish(t, bus.MessageDelete, guildA, channelX, map[string]any{"id": "9"})

	fa := ca.dispatch(bus.MessageCreate)
	fb := cb.dispatch(bus.MessageCreate)
	if fa.Seq != 1 || fb.Seq != 1 {
		t.Fatalf("seq = %d, %d", fa.Seq, fb.Seq)
	}
	var da, db map[string]any
	_ = json.Unmarshal(fa.D, &da)
	_ = json.Unmarshal(fb.D, &db)
	if da["nonce"] != "n-1" || da["text"] != "oi" {
		t.Fatalf("author payload = %v", da)
	}
	if _, has := db["nonce"]; has || db["text"] != "oi" {
		t.Fatalf("other payload = %v", db)
	}
	if f := ca.dispatch(bus.MessageDelete); f.Seq != 2 {
		t.Fatalf("seq = %d", f.Seq)
	}
	cb.dispatch(bus.MessageDelete)
}

// Quem não tem ViewChannel não recebe o evento do canal, mas recebe os da guild.
// O cache segura a resposta até um evento de papel/override/canal/membro invalidá-la.
func TestFanOutFiltersByPermissionAndInvalidatesCache(t *testing.T) {
	pool := dbtest.New(t)
	dir := standardDir()
	dir.setView(bob, channelX, false)
	e := newEnv(t, pool, 1, Config{}, dir)
	listening(t, pool, 1)
	ca, _ := open(t, e, alice)
	cb, _ := open(t, e, bob)

	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "1", "text": "secreto", "author": map[string]any{"id": "1"}})
	e.publish(t, bus.RoleCreate, guildA, 0, map[string]any{"guildId": "1000"}) // guild inteira (e esvazia o cache)
	ca.dispatch(bus.MessageCreate)
	ca.dispatch(bus.RoleCreate)
	cb.dispatch(bus.RoleCreate) // bob só vê o evento da guild; a mensagem não veio
	cb.quiet()

	// o banco passa a permitir, mas o cache ainda diz "não": nenhuma consulta nova, nenhuma entrega
	dir.setView(bob, channelX, true)
	// (o RoleCreate acima esvaziou o cache; este envio repovoa com "sim")
	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "2", "text": "agora sim", "author": map[string]any{"id": "1"}})
	ca.dispatch(bus.MessageCreate)
	cb.dispatch(bus.MessageCreate)

	// revoga no banco; sem evento de invalidação o cache ainda entrega
	dir.setView(bob, channelX, false)
	calls := dir.calls.Load()
	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "3", "author": map[string]any{"id": "1"}})
	ca.dispatch(bus.MessageCreate)
	cb.dispatch(bus.MessageCreate)
	if dir.calls.Load() != calls {
		t.Fatalf("cache miss: %d new lookups", dir.calls.Load()-calls)
	}

	// o override/papel muda: o evento invalida e a próxima mensagem já respeita a revogação (RNF7)
	e.publish(t, bus.ChannelPermissions, guildA, channelX, map[string]any{"channelId": "2000"})
	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "4", "author": map[string]any{"id": "1"}})
	ca.dispatch(bus.ChannelPermissions)
	ca.dispatch(bus.MessageCreate)
	cb.quiet() // channel.permissions é do canal: bob não o vê mais; a mensagem 4 também não
}

func TestGuildEventsTargetMembersAndTrackMembership(t *testing.T) {
	pool := dbtest.New(t)
	dir := standardDir()
	dir.guilds[bob] = nil // bob ainda não está na guild
	e := newEnv(t, pool, 1, Config{}, dir)
	listening(t, pool, 1)
	ca, _ := open(t, e, alice)
	cb, rd := open(t, e, bob)
	if len(rd.Guilds) != 0 {
		t.Fatalf("bob guilds = %v", rd.Guilds)
	}

	e.publish(t, bus.MemberUpdate, guildA, 0, map[string]any{"userId": "9"})
	ca.dispatch(bus.MemberUpdate)
	cb.quiet()

	// bob entra: passa a receber a guild, inclusive o próprio member.join
	e.publish(t, bus.MemberJoin, guildA, 0, map[string]any{"userId": "2"})
	ca.dispatch(bus.MemberJoin)
	cb.dispatch(bus.MemberJoin)
	e.publish(t, bus.RoleUpdate, guildA, 0, map[string]any{})
	ca.dispatch(bus.RoleUpdate)
	cb.dispatch(bus.RoleUpdate)

	// bob sai: recebe o member.leave e nada depois
	e.publish(t, bus.MemberLeave, guildA, 0, map[string]any{"userId": "2", "reason": "kick"})
	ca.dispatch(bus.MemberLeave)
	cb.dispatch(bus.MemberLeave)
	e.publish(t, bus.RoleUpdate, guildA, 0, map[string]any{})
	ca.dispatch(bus.RoleUpdate)
	cb.quiet()

	// guild.create só vai ao dono
	e.publish(t, bus.GuildCreate, 3000, 0, map[string]any{"userId": "2"})
	cb.dispatch(bus.GuildCreate)
	ca.quiet()
	e.publish(t, bus.MemberUpdate, 3000, 0, map[string]any{})
	cb.dispatch(bus.MemberUpdate)
	ca.quiet()

	// guild.delete chega a todos e encerra o roteamento
	e.publish(t, bus.GuildDelete, guildA, 0, map[string]any{"id": "1000"})
	ca.dispatch(bus.GuildDelete)
	e.publish(t, bus.RoleUpdate, guildA, 0, map[string]any{})
	ca.quiet()
}

func TestResumeReplaysMissedEvents(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{}, standardDir())
	listening(t, pool, 1)
	c, rd := open(t, e, alice)
	for i := 1; i <= 3; i++ {
		e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"n": i})
	}
	var last uint64
	for range 3 {
		last = c.dispatch(bus.MessageCreate).Seq
	}
	_ = c.ws.CloseNow()
	waitFor(t, func() bool { return e.gw.Stats().Connections == 0 })

	// eventos enquanto desconectado
	for i := 4; i <= 6; i++ {
		e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"n": i})
	}
	waitFor(t, func() bool { return e.sessionSeq(rd.SessionID) == 6 })

	c2 := dial(t, e.url())
	c2.expect(opHello)
	c2.send(opResume, resumeData{Token: "tok-1", SessionID: rd.SessionID, Seq: last})
	c2.expect(opResumed)
	for want := uint64(4); want <= 6; want++ {
		f := c2.dispatch(bus.MessageCreate)
		if f.Seq != want {
			t.Fatalf("seq = %d, want %d", f.Seq, want)
		}
	}
	// e a sessão segue viva: o próximo evento tem seq 7
	e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"n": 7})
	if f := c2.dispatch(bus.MessageCreate); f.Seq != 7 {
		t.Fatalf("seq = %d", f.Seq)
	}
}

func TestResumeInvalidSession(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{RingSize: 4}, standardDir())
	listening(t, pool, 1)
	c, rd := open(t, e, alice)
	_ = c.ws.CloseNow()
	waitFor(t, func() bool { return e.gw.Stats().Connections == 0 })
	for i := 1; i <= 10; i++ { // estoura o buffer de 4
		e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"n": i})
	}
	waitFor(t, func() bool { return e.sessionSeq(rd.SessionID) == 10 })

	try := func(token, session string, seq uint64) *client {
		c := dial(t, e.url())
		c.expect(opHello)
		c.send(opResume, resumeData{Token: token, SessionID: session, Seq: seq})
		return c
	}
	// seq que já saiu do buffer
	try("tok-1", rd.SessionID, 2).expect(opInvalidSession)
	// seq do futuro
	try("tok-1", rd.SessionID, 99).expect(opInvalidSession)
	// sessão desconhecida
	try("tok-1", "nope", 0).expect(opInvalidSession)
	// sessão de outro usuário
	try("tok-2", rd.SessionID, 8).expect(opInvalidSession)
	// token ruim
	if code := try("bad", rd.SessionID, 8).closeCode(); code != CloseAuthFailed {
		t.Fatalf("close code = %d", code)
	}
	// seq 6 é o limite: o mais antigo guardado é 7
	c3 := try("tok-1", rd.SessionID, 6)
	c3.expect(opResumed)
	for want := uint64(7); want <= 10; want++ {
		if f := c3.dispatch(bus.MessageCreate); f.Seq != want {
			t.Fatalf("seq = %d, want %d", f.Seq, want)
		}
	}

	// depois do invalid_session o cliente pode fazer identify na mesma conexão
	c4 := try("tok-1", "nope", 0)
	c4.expect(opInvalidSession)
	c4.send(opIdentify, identifyData{Token: "tok-1"})
	c4.expect(opReady)
}

func TestSlowClientIsDroppedWithoutBlockingOthers(t *testing.T) {
	pool := dbtest.New(t)
	e := newEnv(t, pool, 1, Config{RingSize: 4, SendQueue: 64, WriteTimeout: 500 * time.Millisecond}, standardDir())
	listening(t, pool, 1)

	// bob identifica e nunca mais lê
	slow := dialRaw(t, e.url())
	ctx := context.Background()
	if _, _, err := slow.Read(ctx); err != nil { // hello
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"op": opIdentify, "d": identifyData{Token: "tok-2"}})
	if err := slow.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := slow.Read(ctx); err != nil { // ready
		t.Fatal(err)
	}
	fast, _ := open(t, e, alice)

	const n = 300
	text := strings.Repeat("x", 100<<10)
	// em lotes menores que a fila: o cliente rápido recebe tudo, em ordem, enquanto o lento trava
	const batch = 20
	for sent := 0; sent < n; sent += batch {
		for i := range batch {
			e.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"n": sent + i, "text": text})
		}
		for i := range batch {
			f := fast.dispatch(bus.MessageCreate)
			if f.Seq != uint64(sent+i+1) {
				t.Fatalf("fast client seq = %d, want %d", f.Seq, sent+i+1)
			}
		}
	}
	waitFor(t, func() bool { return e.gw.Stats().SlowDisconnects == 1 })
	if e.gw.Stats().SlowDisconnects != 1 {
		t.Fatalf("slow disconnects = %d", e.gw.Stats().SlowDisconnects)
	}
}

// Duas réplicas no mesmo Postgres: o evento publicado numa chega aos clientes da outra,
// e a invalidação do cache de permissões também atravessa as réplicas.
func TestTwoGatewayNodes(t *testing.T) {
	poolA := dbtest.New(t)
	poolB, err := db.Open(context.Background(), poolA.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolB.Close)
	dirA, dirB := standardDir(), standardDir()
	a := newEnv(t, poolA, 1, Config{}, dirA)
	b := newEnv(t, poolB, 2, Config{}, dirB)
	listening(t, poolA, 2)

	ca, _ := open(t, a, alice)
	cb, _ := open(t, b, bob)

	a.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "1", "author": map[string]any{"id": "1"}})
	ca.dispatch(bus.MessageCreate)
	cb.dispatch(bus.MessageCreate)
	b.publish(t, bus.MessageUpdate, guildA, channelX, map[string]any{"id": "1"})
	ca.dispatch(bus.MessageUpdate)
	cb.dispatch(bus.MessageUpdate)

	// bob perde o acesso: a mudança é publicada pela réplica A e a B (cache aquecido com "sim") tem que obedecer
	dirB.setView(bob, channelX, false)
	a.publish(t, bus.ChannelPermissions, guildA, channelX, map[string]any{"channelId": "2000"})
	ca.dispatch(bus.ChannelPermissions)
	b.publish(t, bus.MessageCreate, guildA, channelX, map[string]any{"id": "2", "author": map[string]any{"id": "1"}})
	ca.dispatch(bus.MessageCreate)
	cb.quiet()
}

func TestShutdownClosesConnections(t *testing.T) {
	pool := dbtest.New(t)
	b := bus.NewPostgres(pool, ids.NewGenerator(1), bus.Options{})
	gw := New(Config{}, AuthFunc(fakeAuth), standardDir(), b)
	mux := http.NewServeMux()
	gw.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { gw.Run(ctx); close(done) }()
	c := dial(t, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/gateway")
	c.expect(opHello)
	c.send(opIdentify, identifyData{Token: "tok-1"})
	c.expect(opReady)
	cancel()
	<-done
	if code := c.closeCode(); code != websocket.StatusGoingAway {
		t.Fatalf("close code = %d", code)
	}
}

// ---- unidades ----

func TestRingBuffer(t *testing.T) {
	s := newSession(1, 3)
	for range 5 {
		s.deliver("x", json.RawMessage(`{}`))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// guarda 3,4,5
	for after, want := range map[uint64]int{2: 3, 3: 2, 4: 1, 5: 0} {
		fr, ok := s.sinceLocked(after)
		if !ok || len(fr) != want {
			t.Fatalf("since(%d) = %d frames ok=%v, want %d", after, len(fr), ok, want)
		}
	}
	for _, after := range []uint64{0, 1, 6} {
		if _, ok := s.sinceLocked(after); ok {
			t.Fatalf("since(%d) must be invalid", after)
		}
	}
	fr, _ := s.sinceLocked(2)
	if !strings.Contains(string(fr[0]), `"seq":3`) || !strings.Contains(string(fr[2]), `"seq":5`) {
		t.Fatalf("frames out of order: %s", fr)
	}
}

func TestPermCacheTTL(t *testing.T) {
	c := newPermCache(time.Second)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.put(1, 2, 3, true)
	if ok, hit := c.get(1, 2, 3); !hit || !ok {
		t.Fatal("expected hit")
	}
	now = now.Add(2 * time.Second)
	if _, hit := c.get(1, 2, 3); hit {
		t.Fatal("expected expiry")
	}
	c.put(1, 2, 3, true)
	c.invalidateGuild(1)
	if _, hit := c.get(1, 2, 3); hit {
		t.Fatal("expected invalidation")
	}
}

// ---- util ----

func (e *env) sessionSeq(id string) uint64 {
	e.gw.mu.RLock()
	s := e.gw.sessions[id]
	e.gw.mu.RUnlock()
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
