package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"kurz/server/internal/platform/ids"
)

// ServeHTTP faz o upgrade e conduz a conexão: hello, identify/resume e o laço de heartbeat.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	closing := g.closing
	g.mu.RUnlock()
	if closing {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: g.cfg.OriginPatterns})
	if err != nil {
		return // Accept já respondeu
	}
	ws.SetReadLimit(16 << 10)
	c := newConn(ws, g.cfg.SendQueue)
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		_ = ws.Close(1001, "server shutting down")
		return
	}
	g.conns[c] = struct{}{}
	g.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer func() {
		cancel()
		c.kill(websocket.StatusNormalClosure, "", false)
		g.mu.Lock()
		delete(g.conns, c)
		g.mu.Unlock()
	}()

	sess := g.handshake(ctx, c)
	if sess == nil {
		return
	}
	defer sess.detach(c)
	go c.writeLoop(ctx, g.cfg.WriteTimeout, func() { g.slow(c) })
	g.readLoop(ctx, c, sess.userID)
}

func (g *Gateway) fail(c *conn, code websocket.StatusCode, reason string) {
	c.kill(code, reason, true)
}

// handshake manda hello e espera identify ou resume. Um resume recusado manda invalid_session e
// deixa o cliente tentar o identify na mesma conexão. Devolve nil se a conexão acabou.
func (g *Gateway) handshake(ctx context.Context, c *conn) *session {
	if err := c.writeDirect(ctx, g.cfg.WriteTimeout, frame{Op: opHello, D: mustJSON(helloData{HeartbeatInterval: g.cfg.HeartbeatInterval.Milliseconds()})}); err != nil {
		return nil
	}
	// Read com ctx cancelado derruba o socket sem close frame; por isso o prazo é um timer que fecha com código.
	timer := time.AfterFunc(g.cfg.IdentifyTimeout, func() { g.fail(c, CloseIdentifyTimeout, "identify timeout") })
	defer timer.Stop()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return nil
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			g.fail(c, CloseBadFrame, "invalid frame")
			return nil
		}
		switch f.Op {
		case opIdentify:
			var d identifyData
			if json.Unmarshal(f.D, &d) != nil {
				g.fail(c, CloseBadFrame, "invalid identify")
				return nil
			}
			uid, err := g.auth.Authenticate(d.Token)
			if err != nil {
				g.fail(c, CloseAuthFailed, "invalid token")
				return nil
			}
			return g.identify(ctx, c, uid)
		case opResume:
			var d resumeData
			if json.Unmarshal(f.D, &d) != nil {
				g.fail(c, CloseBadFrame, "invalid resume")
				return nil
			}
			uid, err := g.auth.Authenticate(d.Token)
			if err != nil {
				g.fail(c, CloseAuthFailed, "invalid token")
				return nil
			}
			if s := g.resume(c, uid, d); s != nil {
				return s
			}
			if err := c.writeDirect(ctx, g.cfg.WriteTimeout, frame{Op: opInvalidSession}); err != nil {
				return nil
			}
		default:
			g.fail(c, CloseBadFrame, "expected identify or resume")
			return nil
		}
	}
}

// identify cria a sessão: registra primeiro (para não perder eventos), lê as guilds, e só então liga
// a conexão, mandando o ready seguido do que se acumulou no buffer. O que foi commitado antes do
// ready o cliente enxerga por REST depois dele; o que vem depois chega como dispatch.
func (g *Gateway) identify(ctx context.Context, c *conn, uid int64) *session {
	s := newSession(uid, g.cfg.RingSize)
	g.mu.Lock()
	g.sessions[s.id] = s
	g.mu.Unlock()

	gs, err := g.dir.Guilds(ctx, uid)
	if err != nil {
		slog.Error("gateway: load guilds", "user", uid, "err", err)
		g.drop(s)
		g.fail(c, websocket.StatusInternalError, "internal error")
		return nil
	}
	data := readyData{SessionID: s.id, User: userData{ID: ids.Format(uid)}, Guilds: make([]guildData, len(gs)),
		HeartbeatInterval: g.cfg.HeartbeatInterval.Milliseconds()}
	s.mu.Lock()
	for i, gd := range gs {
		s.guilds[gd.ID] = struct{}{}
		data.Guilds[i] = guildData{ID: ids.Format(gd.ID), Name: gd.Name, OwnerID: ids.Format(gd.OwnerID)}
	}
	frames, _ := s.sinceLocked(0)
	s.mu.Unlock()
	if g.voice != nil {
		vs, err := g.voice.Snapshot(ctx, uid)
		if err != nil {
			slog.Error("gateway: load voice state", "user", uid, "err", err)
			g.drop(s)
			g.fail(c, websocket.StatusInternalError, "internal error")
			return nil
		}
		if vs == nil {
			vs = []json.RawMessage{}
		}
		data.Voice = mustJSON(vs)
	}
	ready, _ := json.Marshal(frame{Op: opReady, D: mustJSON(data)})
	if _, ok := s.attach(c, ready, frames); !ok {
		g.drop(s)
		g.fail(c, websocket.StatusInternalError, "internal error")
		return nil
	}
	return s
}

func (g *Gateway) drop(s *session) {
	g.mu.Lock()
	delete(g.sessions, s.id)
	g.mu.Unlock()
}

// resume retoma a sessão se ela é do usuário e o buffer ainda tem tudo o que vem depois de d.Seq.
func (g *Gateway) resume(c *conn, uid int64, d resumeData) *session {
	g.mu.RLock()
	s := g.sessions[d.SessionID]
	g.mu.RUnlock()
	if s == nil || s.userID != uid {
		return nil
	}
	resumed, _ := json.Marshal(frame{Op: opResumed})
	// tudo sob o lock: nenhum evento novo entra entre o buffer lido e a conexão ligada
	s.mu.Lock()
	frames, ok := s.sinceLocked(d.Seq)
	if !ok || len(frames)+1 > cap(c.out) {
		s.mu.Unlock()
		return nil
	}
	old := s.conn
	s.conn = c
	c.enqueue(resumed)
	for _, f := range frames {
		c.enqueue(f)
	}
	s.mu.Unlock()
	if old != nil {
		old.kill(CloseSessionMoved, "session resumed elsewhere", true)
	}
	return s
}

// readLoop trata heartbeats. Silêncio por mais de 2 intervalos derruba a conexão (a sessão segue viva para resume).
func (g *Gateway) readLoop(ctx context.Context, c *conn, uid int64) {
	ack, _ := json.Marshal(frame{Op: opHeartbeatAck})
	timer := time.AfterFunc(2*g.cfg.HeartbeatInterval, func() { g.fail(c, CloseHeartbeatTimeout, "heartbeat timeout") })
	defer timer.Stop()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		timer.Reset(2 * g.cfg.HeartbeatInterval)
		var f frame
		if json.Unmarshal(data, &f) != nil {
			g.fail(c, CloseBadFrame, "unexpected frame")
			return
		}
		switch {
		case f.Op == opHeartbeat:
			if g.voice != nil {
				g.voice.Touch(ctx, uid)
			}
		case f.Op == opVoiceStateSet && g.voice != nil:
			var d voiceStateSetData
			if json.Unmarshal(f.D, &d) != nil {
				g.fail(c, CloseBadFrame, "invalid voice.state.set")
				return
			}
			if err := g.voice.SetState(ctx, uid, d.Muted, d.CamOn, d.Sharing); err != nil && ctx.Err() == nil {
				slog.Error("gateway: voice.state.set", "user", uid, "err", err)
			}
			continue // sem ack: o resultado chega como voice.state
		default:
			g.fail(c, CloseBadFrame, "unexpected frame")
			return
		}
		if !c.enqueue(ack) {
			g.slow(c)
			return
		}
	}
}

func (g *Gateway) slow(c *conn) {
	// conta uma vez por conexão, mesmo que o handler já a tenha encerrado por outro caminho
	if c.slow.CompareAndSwap(false, true) {
		g.slowDisconnects.Add(1)
	}
	c.kill(CloseSlowConsumer, "slow consumer", false)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
