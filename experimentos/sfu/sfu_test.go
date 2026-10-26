package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

var testSecret = []byte("segredo-de-teste")

func token(t *testing.T, user, room, jti string) string {
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Name: "nome-" + user, Room: room,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "kurz-manager", Subject: user, ID: jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(60 * time.Second)),
		},
	}).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type client struct {
	ws     *websocket.Conn
	pc     *webrtc.PeerConnection
	offers chan string // ofertas do servidor, já respondidas
	closed chan error  // erro final de leitura do socket
}

func send(ws *websocket.Conn, op string, d any) error {
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	return ws.Write(context.Background(), websocket.MessageText, b)
}

func readMsg(ws *websocket.Conn) (msg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, b, err := ws.Read(ctx)
	if err != nil {
		return msg{}, err
	}
	var m msg
	return m, json.Unmarshal(b, &m)
}

func dialJoin(t *testing.T, url, tok string) *websocket.Conn {
	ws, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(1 << 20)
	if err := send(ws, "join", map[string]string{"token": tok}); err != nil {
		t.Fatal(err)
	}
	return ws
}

func expect4001(t *testing.T, url, tok string) {
	t.Helper()
	ws := dialJoin(t, url, tok)
	defer ws.CloseNow()
	m, err := readMsg(ws)
	if err != nil || m.Op != "error" {
		t.Fatalf("esperava frame error, veio %+v %v", m, err)
	}
	if _, err := readMsg(ws); websocket.CloseStatus(err) != 4001 {
		t.Fatalf("esperava close 4001, veio %v", err)
	}
}

func connect(t *testing.T, url, tok string) *client {
	t.Helper()
	ws := dialJoin(t, url, tok)
	if m, err := readMsg(ws); err != nil || m.Op != "joined" {
		t.Fatalf("esperava joined, veio %+v %v", m, err)
	}
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		t.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se)).
		NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	c := &client{ws: ws, pc: pc, offers: make(chan string, 16), closed: make(chan error, 1)}
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			_ = send(ws, "candidate", cand.ToJSON())
		}
	})
	go func() {
		for {
			_, b, err := ws.Read(context.Background())
			if err != nil {
				c.closed <- err
				return
			}
			var m msg
			_ = json.Unmarshal(b, &m)
			switch m.Op {
			case "answer":
				var d sdpBody
				_ = json.Unmarshal(m.D, &d)
				_ = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: d.SDP})
			case "offer":
				var d sdpBody
				_ = json.Unmarshal(m.D, &d)
				if pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: d.SDP}) != nil {
					continue
				}
				ans, _ := pc.CreateAnswer(nil)
				_ = pc.SetLocalDescription(ans)
				_ = send(ws, "answer", sdpBody{ans.SDP})
				c.offers <- d.SDP
			case "candidate":
				var d webrtc.ICECandidateInit
				if json.Unmarshal(m.D, &d) == nil && d.Candidate != "" {
					_ = pc.AddICECandidate(d)
				}
			}
		}
	}()
	return c
}

func participants(t *testing.T, base, room string) map[string][3]bool {
	t.Helper()
	res, err := http.Get(base + "/internal/rooms/" + room)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Participants []struct {
			UserID              string
			Mic, Camera, Screen bool
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Participants == nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	out := map[string][3]bool{}
	for _, p := range body.Participants {
		out[p.UserID] = [3]bool{p.Mic, p.Camera, p.Screen}
	}
	return out
}

func TestSFU(t *testing.T) {
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(testSecret, udp, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	pubSrv := httptest.NewServer(s.publicMux())
	intSrv := httptest.NewServer(s.internalMux())
	defer func() { s.stop(); pubSrv.Close(); intSrv.Close(); _ = udp.Close() }()
	url := "ws" + strings.TrimPrefix(pubSrv.URL, "http") + "/v1/media"

	expect4001(t, url, "lixo")
	expect4001(t, url, token(t, "X", "sala", "j0")+"x") // assinatura quebrada

	tokA := token(t, "A", "sala", "jA")
	a := connect(t, url, tokA)
	defer a.pc.Close()
	expect4001(t, url, tokA) // jti repetido

	// A publica mic (Opus) e camera (VP8) com ids locais quaisquer.
	mic, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "local-mic", "local-stream")
	// A câmera vai em RTP cru com uma extensão de cabeçalho (como o MID do libwebrtc): o SFU tem de tirá-la ao repassar.
	cam, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "local-cam", "local-stream")
	var camSender *webrtc.RTPSender
	for _, tr := range []webrtc.TrackLocal{mic, cam} {
		snd, err := a.pc.AddTrack(tr)
		if err != nil {
			t.Fatal(err)
		}
		if tr == cam {
			camSender = snd
		}
	}
	_ = send(a.ws, "publish", map[string]string{"trackId": "local-mic", "source": "mic"})
	_ = send(a.ws, "publish", map[string]string{"trackId": "local-cam", "source": "camera"})
	offer, _ := a.pc.CreateOffer(nil)
	_ = a.pc.SetLocalDescription(offer)
	_ = send(a.ws, "offer", sdpBody{offer.SDP})

	stopMedia := make(chan struct{})
	defer close(stopMedia)
	go func() {
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		var seq uint16
		for {
			select {
			case <-stopMedia:
				return
			case <-tk.C:
				_ = mic.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
				seq++
				pkt := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 1800},
					Payload: []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a}}
				_ = pkt.SetExtension(1, []byte{0xab})
				_ = cam.WriteRTP(pkt)
			}
		}
	}()

	b := connect(t, url, token(t, "B", "sala", "jB"))
	defer b.pc.Close()
	got := make(chan string, 4)
	camExt := make(chan bool, 1) // o primeiro pacote de vídeo de B tinha extensão de cabeçalho?
	b.pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if pkt, _, err := tr.ReadRTP(); err == nil {
			got <- tr.StreamID() + "|" + tr.ID()
			if tr.Kind() == webrtc.RTPCodecTypeVideo {
				camExt <- pkt.Extension
			}
		}
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
		}
	})
	want := map[string]bool{"A|A:mic": true, "A|A:camera": true}
	for len(want) > 0 {
		select {
		case k := <-got:
			if !want[k] {
				t.Fatalf("track inesperada %q", k)
			}
			delete(want, k)
		case <-time.After(15 * time.Second):
			t.Fatalf("B não recebeu RTP de %v", want)
		}
	}

	select {
	case ext := <-camExt:
		if ext {
			t.Fatal("o SFU repassou a extensão de cabeçalho do publicador (o MID confunde o demux do destinatário)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sem pacote de vídeo em B")
	}

	ps := participants(t, intSrv.URL, "sala")
	if ps["A"] != [3]bool{true, true, false} || ps["B"] != [3]bool{} || len(ps) != 2 {
		t.Fatalf("participantes errados: %v", ps)
	}
	if len(participants(t, intSrv.URL, "nenhuma")) != 0 {
		t.Fatal("sala inexistente deveria vir vazia")
	}

	// A desliga a câmera com a conexão aberta: o servidor despublica e B recebe oferta sem A:camera (mic fica).
	if err := a.pc.RemoveTrack(camSender); err != nil {
		t.Fatal(err)
	}
	off2, _ := a.pc.CreateOffer(nil)
	_ = a.pc.SetLocalDescription(off2)
	_ = send(a.ws, "offer", sdpBody{off2.SDP})
	for i := 0; participants(t, intSrv.URL, "sala")["A"] != [3]bool{true, false, false}; i++ {
		if i > 200 {
			t.Fatalf("A desligou a câmera e o servidor continua publicando: %v", participants(t, intSrv.URL, "sala"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // deixa a renegociação com B terminar
	last := ""
	for drained := false; !drained; { // ofertas antigas ainda na fila: vale a última
		select {
		case last = <-b.offers:
		default:
			drained = true
		}
	}
	if strings.Contains(last, "A:camera") || !strings.Contains(last, "A:mic") {
		t.Fatalf("última oferta a B deveria ter A:mic e não A:camera:\n%s", last)
	}

	// A sai: B recebe uma oferta nova sem as tracks de A.
	_ = a.ws.Close(websocket.StatusNormalClosure, "")
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case sdp := <-b.offers:
			done = !strings.Contains(sdp, "A:mic") && !strings.Contains(sdp, "A:camera")
		case <-deadline:
			t.Fatal("B não recebeu oferta sem as tracks de A")
		}
	}
	if ps := participants(t, intSrv.URL, "sala"); len(ps) != 1 {
		t.Fatalf("depois da saída de A: %v", ps)
	}

	// Mesmo userId entrando de novo derruba a sessão antiga.
	b2 := connect(t, url, token(t, "B", "sala", "jB2"))
	defer b2.pc.Close()
	select {
	case err := <-b.closed:
		if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
			t.Fatalf("sessão antiga fechou com %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sessão antiga de B não caiu")
	}

	_ = b2.ws.Close(websocket.StatusNormalClosure, "")
	for i := 0; ; i++ {
		s.mu.Lock()
		n := len(s.rooms)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if i > 100 {
			t.Fatal("sala não sumiu depois que todos saíram")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
