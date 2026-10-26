package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

var (
	errShutdown  = errors.New("server shutting down")
	errReplaced  = errors.New("same user joined again")
	errQueueFull = errors.New("send queue full")
	errPCFailed  = errors.New("peer connection failed")
	errGone      = errors.New("socket closed")
	sources      = map[string]bool{"mic": true, "camera": true, "screen": true}
)

// ponytail: um mutex global para salas, peers e publicações; por sala se virar gargalo.
type server struct {
	secret []byte
	turn   *turnSource // nil: ICE lite, só candidato host
	api    *webrtc.API
	ctx    context.Context
	cancel context.CancelCauseFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	rooms map[string]map[string]*peer // sala -> userId -> peer
	jtis  map[string]time.Time        // jti usado -> exp
}

type peer struct {
	s            *server
	room         string
	userID, name string
	pc           *webrtc.PeerConnection
	ctx          context.Context
	cancel       context.CancelCauseFunc
	out          chan []byte   // fila limitada; só o loop escreve no socket
	neg          chan struct{} // cap 1: pedidos de renegociação coalescem
	done         chan struct{} // fechado depois do leave

	// protegidos por s.mu
	declared map[string]string // trackId -> source (frame publish)
	pubs     map[string]*pub   // source -> publicação
}

type pub struct {
	owner  *peer
	source string
	remote *webrtc.TrackRemote
	local  *webrtc.TrackLocalStaticRTP // uma por publicação, com um binding por assinante
	subs   map[*peer]*webrtc.RTPSender // s.mu
}

type msg struct {
	Op string          `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
}

type sdpBody struct {
	SDP string `json:"sdp"`
}

type candBody struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type claims struct {
	Name string `json:"name"`
	Room string `json:"room"`
	jwt.RegisteredClaims
}

func newServer(secret []byte, udp net.PacketConn, publicIP string, turn *turnSource) (*server, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	// ponytail: sem simulcast e sem REMB/controle de banda; uma camada VP8 por publicador.
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}}},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	// Não usa RegisterDefaultInterceptors nem TWCC: eles anunciam extensões de cabeçalho (mid, rid, transport-cc) com ids
	// fixos que colidem com os do libwebrtc quando o cliente adiciona uma m-line ("codec collision for header extension id").
	// ponytail: sem TWCC o publicador não estima banda pelo servidor; reavaliar se a qualidade do vídeo/tela ficar baixa.
	ir := &interceptor.Registry{}
	if err := webrtc.ConfigureNack(m, ir); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureRTCPReports(ir); err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	// Sem TURN: ICE lite, o cliente controla o ICE e o servidor só responde aos checks.
	// Com TURN: ICE completo, porque o agente lite não junta candidato relay. O relay é o que deixa um cliente de fora
	// chegar ao nó sem porta aberta: os dois lados alocam no TURN da Cloudflare com conexões de saída.
	se.SetLite(turn == nil)
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetIncludeLoopbackCandidate(true) // permite rodar local em 127.0.0.1
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	if publicIP != "" {
		// equivale a SetNAT1To1IPs(ips, Host), que está deprecated no pion 4.2
		if err := se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External: []string{publicIP}, AsCandidateType: webrtc.ICECandidateTypeHost,
		}); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	return &server{
		secret: secret,
		turn:   turn,
		api:    webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se)),
		ctx:    ctx,
		cancel: cancel,
		rooms:  map[string]map[string]*peer{},
		jtis:   map[string]time.Time{},
	}, nil
}

// iceServers devolve um único URL TURN sobre UDP (o SettingEngine só usa UDP4; mais URLs = alocações redundantes).
// Sem TURN ou com falha, nil: o par segue só com o candidato host.
func (s *server) iceServers(ctx context.Context) []webrtc.ICEServer {
	if s.turn == nil {
		return nil
	}
	servers, err := s.turn.get(ctx)
	if err != nil {
		log.Printf("turn indisponível: %v", err)
		return nil
	}
	for _, is := range servers {
		for _, u := range is.URLs {
			if strings.HasPrefix(u, "turn:") && strings.Contains(u, "transport=udp") {
				return []webrtc.ICEServer{{URLs: []string{u}, Username: is.Username, Credential: is.Credential}}
			}
		}
	}
	log.Print("turn: nenhum URL turn: sobre UDP na resposta")
	return nil
}

func (s *server) publicMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/media", s.media)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return mux
}

func (s *server) internalMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/rooms/{room}", s.roomInfo)
	return mux
}

// stop derruba todas as sessões e espera os handlers terminarem.
func (s *server) stop() {
	s.cancel(errShutdown)
	s.wg.Wait()
}

func (s *server) roomInfo(w http.ResponseWriter, r *http.Request) {
	type participant struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
		Mic    bool   `json:"mic"`
		Camera bool   `json:"camera"`
		Screen bool   `json:"screen"`
	}
	list := []participant{}
	s.mu.Lock()
	for _, p := range s.rooms[r.PathValue("room")] {
		list = append(list, participant{p.userID, p.name, p.pubs["mic"] != nil, p.pubs["camera"] != nil, p.pubs["screen"] != nil})
	}
	s.mu.Unlock()
	slices.SortFunc(list, func(a, b participant) int { return strings.Compare(a.UserID, b.UserID) })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"participants": list})
}

func (s *server) media(w http.ResponseWriter, r *http.Request) {
	s.wg.Add(1)
	defer s.wg.Done()
	// Sem checagem de Origin: o cliente é desktop e o token já autoriza.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(1 << 20) // SDP com várias m-lines passa do limite padrão de 32 KiB

	ctx, cancel := context.WithCancelCause(s.ctx)
	defer cancel(nil)
	in := make(chan msg)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer cancel(errGone)
		for {
			_, b, err := ws.Read(context.Background()) // termina quando o socket fecha
			if err != nil {
				return
			}
			var m msg
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			select {
			case in <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { _ = ws.CloseNow(); <-readDone }()

	var first msg
	select {
	case first = <-in:
	case <-time.After(10 * time.Second):
		first = msg{Op: "timeout"}
	case <-ctx.Done():
		return
	}
	c, err := s.auth(first)
	if err != nil {
		code := "unauthorized"
		if errors.Is(err, jwt.ErrTokenExpired) {
			code = "token_expired"
		}
		writeNow(ws, "error", errBody{code, err.Error()})
		_ = ws.Close(4001, code)
		return
	}

	p := &peer{
		s: s, room: c.Room, userID: c.Subject, name: c.Name,
		ctx: ctx, cancel: cancel,
		out: make(chan []byte, 256), neg: make(chan struct{}, 1), done: make(chan struct{}),
		declared: map[string]string{}, pubs: map[string]*pub{},
	}
	if p.pc, err = s.api.NewPeerConnection(webrtc.Configuration{ICEServers: s.iceServers(ctx)}); err != nil {
		writeNow(ws, "error", errBody{"internal", err.Error()})
		_ = ws.Close(websocket.StatusInternalError, "internal")
		return
	}
	p.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			p.send("candidate", candBody{}) // vazio = fim dos candidatos
			return
		}
		j := c.ToJSON()
		p.send("candidate", candBody{j.Candidate, j.SDPMid, j.SDPMLineIndex})
	})
	p.pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed {
			p.cancel(errPCFailed)
		}
	})
	p.pc.OnTrack(p.onTrack)

	p.send("joined", struct{}{})
	s.join(p)
	p.run(ws, in)
	s.leave(p)
	close(p.done)

	switch cause := context.Cause(ctx); cause {
	case errGone:
	case errShutdown:
		_ = ws.Close(websocket.StatusGoingAway, "shutdown")
	case errReplaced:
		writeNow(ws, "error", errBody{"replaced", cause.Error()})
		_ = ws.Close(websocket.StatusNormalClosure, "replaced")
	default:
		writeNow(ws, "error", errBody{"internal", cause.Error()})
		_ = ws.Close(websocket.StatusInternalError, "internal")
	}
}

func writeNow(ws *websocket.Conn, op string, d any) {
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, b)
}

func (s *server) auth(m msg) (*claims, error) {
	if m.Op != "join" {
		return nil, errors.New("first frame must be join within 10 s")
	}
	var d struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(m.D, &d)
	c := &claims{}
	_, err := jwt.ParseWithClaims(d.Token, c, func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kurz-manager"), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if c.Subject == "" || c.Room == "" || c.ID == "" {
		return nil, errors.New("token without sub, room or jti")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// ponytail: varredura O(n) por join; tokens vivem 60 s, então n fica pequeno
	for j, exp := range s.jtis {
		if now.After(exp) {
			delete(s.jtis, j)
		}
	}
	if _, used := s.jtis[c.ID]; used {
		return nil, errors.New("token already used")
	}
	s.jtis[c.ID] = c.ExpiresAt.Time
	return c, nil
}

// join põe o peer na sala (derrubando a sessão antiga do mesmo userId) e assina o que já está publicado.
func (s *server) join(p *peer) {
	for {
		s.mu.Lock()
		room := s.rooms[p.room]
		if room == nil {
			room = map[string]*peer{}
			s.rooms[p.room] = room
		}
		old := room[p.userID]
		if old == nil {
			room[p.userID] = p
			for _, q := range room {
				for _, pb := range q.pubs {
					if q != p {
						s.subscribe(p, pb)
					}
				}
			}
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		old.cancel(errReplaced)
		<-old.done
	}
}

func (s *server) leave(p *peer) {
	s.mu.Lock()
	room := s.rooms[p.room]
	if room[p.userID] == p {
		delete(room, p.userID)
	}
	for _, q := range room {
		for _, pb := range q.pubs {
			delete(pb.subs, p)
		}
	}
	if len(room) == 0 {
		delete(s.rooms, p.room)
	}
	s.mu.Unlock()
	// Fechar a PC encerra as tracks remotas; cada forward chama unpublish e renegocia os outros.
	_ = p.pc.Close()
}

// subscribe adiciona pb à PC de q. Chamar com s.mu.
func (s *server) subscribe(q *peer, pb *pub) {
	// ponytail: m-line nova a cada assinatura; m-lines inativas acumulam numa sessão longa com muita troca
	tr, err := q.pc.AddTransceiverFromTrack(pb.local, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return
	}
	sender := tr.Sender()
	pb.subs[q] = sender
	go pb.readRTCP(sender)
	if pb.remote.Kind() == webrtc.RTPCodecTypeVideo {
		pb.pli()
	}
	q.renegotiate()
}

func (s *server) unpublish(pb *pub) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pb.owner.pubs[pb.source] == pb {
		delete(pb.owner.pubs, pb.source)
	}
	for q, sender := range pb.subs {
		_ = q.pc.RemoveTrack(sender)
		q.renegotiate()
	}
	clear(pb.subs)
}

func (p *peer) onTrack(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	s := p.s
	s.mu.Lock()
	source, ok := p.declared[remote.ID()]
	room := s.rooms[p.room]
	if !ok || room[p.userID] != p {
		s.mu.Unlock()
		log.Printf("sfu: %s: track %q sem publish, ignorada", p.userID, remote.ID())
		return
	}
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, p.userID+":"+source, p.userID)
	if err != nil {
		s.mu.Unlock()
		return
	}
	pb := &pub{owner: p, source: source, remote: remote, local: local, subs: map[*peer]*webrtc.RTPSender{}}
	old := p.pubs[source]
	p.pubs[source] = pb
	for _, q := range room {
		if q != p {
			s.subscribe(q, pb)
		}
	}
	s.mu.Unlock()
	if old != nil {
		s.unpublish(old) // mesma fonte republicada com outra track
	}

	done := make(chan struct{})
	defer func() { close(done); s.unpublish(pb) }()
	if remote.Kind() == webrtc.RTPCodecTypeVideo {
		go func() {
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					pb.pli()
				case <-done:
					return
				}
			}
		}()
	}
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return // receiver parado (parou de publicar) ou PC fechada
		}
		// As extensões são do publicador (MID, rid): no destinatário o libwebrtc desmultiplexa pelo MID antes do SSRC e erra a m-line.
		pkt.Header.Extension, pkt.Header.Extensions = false, nil
		_ = local.WriteRTP(pkt)
	}
}

func (pb *pub) pli() {
	_ = pb.owner.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(pb.remote.SSRC())}})
}

// readRTCP drena o RTCP do assinante (senão o buffer enche) e repassa PLI/FIR ao publicador.
// ponytail: FIR vira PLI e não há limite de taxa; com muitos assinantes o publicador recebe mais pedidos.
func (pb *pub) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, k := range pkts {
			switch k.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				pb.pli()
			}
		}
	}
}

func (p *peer) renegotiate() {
	select {
	case p.neg <- struct{}{}:
	default:
	}
}

func (p *peer) send(op string, d any) {
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	select {
	case p.out <- b:
	default:
		p.cancel(errQueueFull)
	}
}

// run é o único escritor do socket e serializa toda a negociação SDP do peer.
// Servidor é o par impolite: oferta do cliente durante oferta nossa pendente é ignorada.
func (p *peer) run(ws *websocket.Conn, in <-chan msg) {
	pending, want := false, false
	for {
		select {
		case <-p.ctx.Done():
			return
		case b := <-p.out:
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			err := ws.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				p.cancel(errGone)
				return
			}
		case <-p.neg:
			want = true
		case m := <-in:
			if err := p.handle(m, &pending); err != nil {
				p.cancel(err)
				return
			}
		}
		if want && !pending {
			want = false
			if err := p.offer(); err != nil {
				p.cancel(err)
				return
			}
			pending = true
		}
	}
}

func (p *peer) handle(m msg, pending *bool) error {
	switch m.Op {
	case "publish":
		var d struct{ TrackID, Source string }
		if json.Unmarshal(m.D, &d) != nil || d.TrackID == "" || !sources[d.Source] {
			return nil
		}
		p.s.mu.Lock()
		p.declared[d.TrackID] = d.Source
		p.s.mu.Unlock()
	case "offer":
		if *pending {
			return nil // colisão: o cliente faz rollback, responde a nossa e reoferta
		}
		var d sdpBody
		_ = json.Unmarshal(m.D, &d)
		if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: d.SDP}); err != nil {
			return err
		}
		ans, err := p.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		if err := p.pc.SetLocalDescription(ans); err != nil {
			return err
		}
		p.send("answer", sdpBody{ans.SDP})
	case "answer":
		if !*pending {
			return nil
		}
		var d sdpBody
		_ = json.Unmarshal(m.D, &d)
		if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: d.SDP}); err != nil {
			return err
		}
		*pending = false
	case "candidate":
		var d candBody
		if json.Unmarshal(m.D, &d) != nil || d.Candidate == "" || p.pc.RemoteDescription() == nil {
			return nil // ICE lite: o endereço do cliente chega pelos próprios checks; com TURN, o relay do cliente chega aqui
		}
		_ = p.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: d.Candidate, SDPMid: d.SDPMid, SDPMLineIndex: d.SDPMLineIndex})
	}
	return nil
}

func (p *peer) offer() error {
	o, err := p.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(o); err != nil {
		return err
	}
	p.send("offer", sdpBody{o.SDP})
	return nil
}
