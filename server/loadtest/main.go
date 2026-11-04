// Comando loadtest: bots que registram usuários, entram numa guild, conectam ao gateway, mandam mensagens a uma
// taxa-alvo e verificam RNF1 (entrega ponta a ponta) e RNF2 (latência REST). Sai com código 1 se algo violar.
//
//	go run ./loadtest --url http://localhost:8080 --users 20 --rate 20 --duration 30s
//
// Rode o servidor com KURZ_RATELIMIT=off, senão o rate limit (RNF10) vai barrar o gerador.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type pending struct {
	start    time.Time
	receiver int
}

type runner struct {
	base  string
	hc    *http.Client
	bots  []*bot
	chID  string
	errs  atomic.Int64
	e2e   samples
	read  samples
	write samples

	pend sync.Map // token -> pending
}

func main() {
	url := flag.String("url", "http://localhost:8080", "base URL do servidor")
	users := flag.Int("users", 20, "bots (usuários e sockets)")
	rate := flag.Float64("rate", 20, "mensagens por segundo, no total")
	readRate := flag.Float64("read-rate", 0, "leituras REST por segundo (0 = igual a --rate)")
	duration := flag.Duration("duration", 30*time.Second, "duração da fase de carga")
	chaos := flag.Bool("chaos", false, "modo de falha (RNF8): tolera perda e erros enquanto réplicas morrem; exige que todos os sockets voltem")
	flag.Parse()
	if *users < 2 {
		log.Fatal("--users precisa ser >= 2 (um remetente e um receptor)")
	}
	if *readRate == 0 {
		*readRate = *rate
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	r := &runner{base: strings.TrimRight(*url, "/"), hc: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 512, MaxIdleConns: 512}}}
	if err := r.setup(ctx, *users); err != nil {
		log.Printf("setup falhou: %v", err)
		os.Exit(2)
	}
	log.Printf("pronto: %d bots conectados; carga: %.0f msg/s e %.0f leituras/s por %s%s", *users, *rate, *readRate, *duration, map[bool]string{true: " (modo chaos)"}[*chaos])

	sent := r.load(ctx, *rate, *readRate, *duration)
	// espera os eventos em trânsito
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && r.pendingCount() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	missed := r.pendingCount()
	connected, reconnects := 0, int64(0)
	var rec samples
	for _, b := range r.bots {
		if b.connected.Load() {
			connected++
		}
		reconnects += b.reconnects.Load()
		for _, d := range b.reconnectTimes() {
			rec.add(d)
		}
	}
	stop()

	e2e, rd, wr := r.e2e.stats(), r.read.stats(), r.write.stats()
	fmt.Printf("\nmensagens enviadas: %d (ok %d), eventos perdidos: %d, erros de requisição: %d\n", sent, wr.N, missed, r.errs.Load())
	fmt.Printf("escrita REST   %s\nleitura REST   %s\nponta a ponta  %s\n", wr, rd, e2e)
	if reconnects > 0 {
		fmt.Printf("reconexões: %d, tempo até o ready: %s\n", reconnects, rec.stats())
	}
	cs := verdict(e2e, rd, wr, missed, int(r.errs.Load()), sent, *chaos, connected, *users)
	fmt.Println()
	for _, c := range cs {
		status := "PASS"
		if !c.OK {
			status = "FAIL"
		}
		fmt.Printf("%s  %-26s got %-14s want %s\n", status, c.Name, c.Got, c.Want)
	}
	if !allOK(cs) {
		fmt.Println("\nRESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS")
}

func (r *runner) pendingCount() int {
	n := 0
	r.pend.Range(func(_, _ any) bool { n++; return true })
	return n
}

// call faz uma requisição JSON e devolve o status e o corpo decodificado.
func (r *runner) call(ctx context.Context, method, path, token string, body any) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := r.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, nil
}

func (r *runner) setup(ctx context.Context, n int) error {
	run := time.Now().UnixNano() % 1_000_000_000
	r.bots = make([]*bot, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16) // Argon2 é caro de propósito: não registre todos de uma vez
	errc := make(chan error, n)
	for i := range r.bots {
		b := &bot{r: r, idx: i, name: fmt.Sprintf("lt%d_%d", run, i)}
		r.bots[i] = b
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, out, err := r.call(ctx, "POST", "/v1/auth/register", "", map[string]string{"username": b.name, "password": "loadtest-password"})
			if err != nil || st != 201 {
				errc <- fmt.Errorf("register %s: status %d err %v body %v", b.name, st, err, out)
				return
			}
			b.token = out["accessToken"].(string)
		}()
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		return err
	}
	owner := r.bots[0]
	st, g, err := r.call(ctx, "POST", "/v1/guilds", owner.token, map[string]string{"name": fmt.Sprintf("loadtest-%d", run)})
	if err != nil || st != 201 {
		return fmt.Errorf("create guild: %d %v %v", st, err, g)
	}
	gid := g["id"].(string)
	st, inv, err := r.call(ctx, "POST", "/v1/guilds/"+gid+"/invites", owner.token, map[string]any{})
	if err != nil || st != 201 {
		return fmt.Errorf("create invite: %d %v %v", st, err, inv)
	}
	code := inv["code"].(string)
	for _, b := range r.bots[1:] {
		if st, out, err := r.call(ctx, "POST", "/v1/invites/"+code+"/join", b.token, nil); err != nil || st != 200 {
			return fmt.Errorf("join: %d %v %v", st, err, out)
		}
	}
	st, chs, err := r.call(ctx, "GET", "/v1/guilds/"+gid+"/channels", owner.token, nil)
	if err != nil || st != 200 {
		return fmt.Errorf("list channels: %d %v", st, err)
	}
	for _, c := range chs["items"].([]any) {
		if c := c.(map[string]any); c["kind"] == "text" {
			r.chID = c["id"].(string)
			break
		}
	}
	if r.chID == "" {
		return fmt.Errorf("guild has no text channel")
	}
	// sockets; as conexões só valem depois do ready
	ctx2, cancel := context.WithCancel(context.Background()) // vive até o fim do processo
	_ = cancel
	for _, b := range r.bots {
		go b.run(ctx2)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		ok := 0
		for _, b := range r.bots {
			if b.connected.Load() {
				ok++
			}
		}
		if ok == n {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("only %d/%d sockets reached ready", ok, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// load gera a carga e devolve quantas mensagens tentou enviar.
func (r *runner) load(ctx context.Context, rate, readRate float64, d time.Duration) int {
	var wg sync.WaitGroup
	var sent atomic.Int64
	sem := make(chan struct{}, 512)
	start := time.Now()
	pace := func(rate float64, fn func(i int)) {
		defer wg.Done()
		for i := 0; ; i++ {
			at := start.Add(time.Duration(float64(i) / rate * float64(time.Second)))
			if at.Sub(start) >= d || ctx.Err() != nil {
				return
			}
			time.Sleep(time.Until(at))
			select {
			case sem <- struct{}{}:
			default: // gerador saturado: pula em vez de empilhar (e registra como erro)
				r.errs.Add(1)
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-sem }(); fn(i) }()
		}
	}
	wg.Add(2)
	go pace(rate, func(i int) {
		sent.Add(1)
		s := i % len(r.bots)
		recv := (s + 1) % len(r.bots)
		tok := fmt.Sprintf("lt-%d-%d", start.UnixNano(), i)
		t0 := time.Now()
		r.pend.Store(tok, pending{start: t0, receiver: recv})
		st, _, err := r.call(ctx, "POST", "/v1/channels/"+r.chID+"/messages", r.bots[s].token, map[string]string{"text": tok})
		if err != nil || st != 201 {
			r.pend.Delete(tok)
			r.errs.Add(1)
			return
		}
		r.write.add(time.Since(t0))
	})
	go pace(readRate, func(i int) {
		t0 := time.Now()
		st, _, err := r.call(ctx, "GET", "/v1/channels/"+r.chID+"/messages?limit=50", r.bots[i%len(r.bots)].token, nil)
		if err != nil || st != 200 {
			r.errs.Add(1)
			return
		}
		r.read.add(time.Since(t0))
	})
	wg.Wait()
	return int(sent.Load())
}

// bot é um usuário com um socket no gateway que reconecta sozinho (backoff + jitter, resume quando dá).
type bot struct {
	r     *runner
	idx   int
	name  string
	token string

	connected  atomic.Bool
	reconnects atomic.Int64
	mu         sync.Mutex
	recTimes   []time.Duration
	sessionID  string
	lastSeq    uint64
}

func (b *bot) reconnectTimes() []time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]time.Duration(nil), b.recTimes...)
}

func (b *bot) run(ctx context.Context) {
	attempt := 0
	var lostAt time.Time
	for ctx.Err() == nil {
		ready, err := b.session(ctx, &lostAt)
		b.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		if ready {
			attempt = 0
			if lostAt.IsZero() {
				lostAt = time.Now()
			}
		}
		_ = err
		b.reconnects.Add(1)
		select {
		case <-time.After(backoff(attempt, 250*time.Millisecond, 5*time.Second)):
		case <-ctx.Done():
			return
		}
		attempt++
	}
}

type frame struct {
	Op string          `json:"op"`
	T  string          `json:"t"`
	S  uint64          `json:"seq"`
	D  json.RawMessage `json:"d"`
}

// session abre um socket e lê até ele cair. ready diz se chegou a ficar pronto (zera o backoff).
func (b *bot) session(ctx context.Context, lostAt *time.Time) (ready bool, err error) {
	wsURL := "ws" + strings.TrimPrefix(b.r.base, "http") + "/v1/gateway"
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ws, _, err := websocket.Dial(dctx, wsURL, nil)
	cancel()
	if err != nil {
		return false, err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	send := func(v any) error {
		bs, _ := json.Marshal(v)
		wctx, c := context.WithTimeout(ctx, 5*time.Second)
		defer c()
		return ws.Write(wctx, websocket.MessageText, bs)
	}
	identify := func() error {
		return send(map[string]any{"op": "identify", "d": map[string]string{"token": b.token}})
	}
	hb := 30 * time.Second
	hbStop := make(chan struct{})
	defer close(hbStop)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return ready, err
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Op {
		case "hello":
			var h struct {
				HeartbeatInterval int `json:"heartbeatInterval"`
			}
			_ = json.Unmarshal(f.D, &h)
			if h.HeartbeatInterval > 0 {
				hb = time.Duration(h.HeartbeatInterval) * time.Millisecond
			}
			b.mu.Lock()
			sid, seq := b.sessionID, b.lastSeq
			b.mu.Unlock()
			if sid != "" {
				err = send(map[string]any{"op": "resume", "d": map[string]any{"token": b.token, "sessionId": sid, "seq": seq}})
			} else {
				err = identify()
			}
			if err != nil {
				return ready, err
			}
		case "invalid_session":
			b.mu.Lock()
			b.sessionID, b.lastSeq = "", 0
			b.mu.Unlock()
			if err := identify(); err != nil {
				return ready, err
			}
		case "ready", "resumed":
			if f.Op == "ready" {
				var rd struct {
					SessionID string `json:"sessionId"`
				}
				_ = json.Unmarshal(f.D, &rd)
				b.mu.Lock()
				b.sessionID, b.lastSeq = rd.SessionID, 0
				b.mu.Unlock()
			}
			ready = true
			b.mu.Lock()
			if !lostAt.IsZero() {
				b.recTimes = append(b.recTimes, time.Since(*lostAt))
				*lostAt = time.Time{}
			}
			b.mu.Unlock()
			b.connected.Store(true)
			go func() {
				tk := time.NewTicker(hb)
				defer tk.Stop()
				for {
					select {
					case <-tk.C:
						if send(map[string]string{"op": "heartbeat"}) != nil {
							return
						}
					case <-hbStop:
						return
					case <-ctx.Done():
						return
					}
				}
			}()
		case "dispatch":
			now := time.Now()
			b.mu.Lock()
			b.lastSeq = f.S
			b.mu.Unlock()
			if f.T != "message.create" {
				continue
			}
			var m struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(f.D, &m)
			if v, ok := b.r.pend.Load(m.Text); ok && v.(pending).receiver == b.idx {
				b.r.pend.Delete(m.Text)
				b.r.e2e.add(now.Sub(v.(pending).start))
			}
		}
	}
}
