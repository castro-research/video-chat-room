// Package voice cuida da voz (RF17): entrar/sair de canais de voz, o token de mídia do SFU e o estado `voice.state`.
// Não importa guilds nem channels: o acesso ao canal entra pela interface Channels, adaptada em server.go.
package voice

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/jobs"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

// Channel é o que a voz precisa saber de um canal e das permissões do usuário nele.
type Channel struct {
	ID, GuildID int64
	Voice       bool // kind=voice
	Connect     bool
	Speak       bool
}

// Channels responde sobre canais. VoiceAccess devolve 404 (httpx.ErrNotFound) se o usuário não vê o canal.
type Channels interface {
	VoiceAccess(ctx context.Context, userID, channelID int64) (Channel, error)
	// VisibleChannels devolve os ids dos canais da guild em que o usuário tem ViewChannel, numa consulta agregada por guild.
	VisibleChannels(ctx context.Context, userID, guildID int64) (map[int64]bool, error)
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Config liga a voz. Sem Secret ou SFUURL ela fica desligada (Enabled() == false) e os endpoints respondem 501.
type Config struct {
	Secret     []byte // KURZ_SECRET, o mesmo do SFU
	SFUURL     string // ws:// ou wss:// do /v1/media do SFU, como o CLIENTE alcança
	ICEServers []ICEServer
	TTL        time.Duration // sem heartbeat por este tempo o estado é varrido (padrão 3 min)
	Now        func() time.Time
}

func (c Config) Enabled() bool { return len(c.Secret) > 0 && c.SFUURL != "" }

type Service struct {
	pool *pgxpool.Pool
	ids  *ids.Generator
	ch   Channels
	cfg  Config
}

func NewService(pool *pgxpool.Pool, gen *ids.Generator, ch Channels, cfg Config) *Service {
	if cfg.TTL <= 0 {
		cfg.TTL = 3 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if len(cfg.ICEServers) == 0 {
		cfg.ICEServers = []ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	}
	return &Service{pool: pool, ids: gen, ch: ch, cfg: cfg}
}

func (s *Service) Enabled() bool { return s.cfg.Enabled() }

var errNotSupported = httpx.NewError(http.StatusNotImplemented, "not_supported", "voice is not configured on this server")

// ---- views (formato do contrato) ----

type UserView struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Subtitle string   `json:"subtitle,omitempty"`
	Hue      *float64 `json:"hue,omitempty"`
}

type ParticipantView struct {
	User    UserView `json:"user"`
	Muted   bool     `json:"muted"`
	CamOn   bool     `json:"camOn"`
	Sharing bool     `json:"sharing"`
}

type SessionView struct {
	Media struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"media"`
	ICEServers   []ICEServer       `json:"iceServers"`
	Participants []ParticipantView `json:"participants"`
}

func participant(r row) ParticipantView {
	// presença ainda não existe no servidor: quem está em voz aparece como online
	return ParticipantView{User: UserView{ID: ids.Format(r.UserID), Username: r.Username, Name: r.Name, Status: "online",
		Subtitle: r.Subtitle, Hue: r.Hue}, Muted: r.Muted, CamOn: r.CamOn, Sharing: r.Sharing}
}

func participants(rs []row) []ParticipantView {
	out := make([]ParticipantView, len(rs))
	for i, r := range rs {
		out[i] = participant(r)
	}
	return out
}

// statePayload é o `d` do voice.state (VoiceStateEvent).
func statePayload(r row, connected bool) map[string]any {
	return map[string]any{"channelId": ids.Format(r.ChannelID), "connected": connected, "participant": participant(r)}
}

func (s *Service) emit(ctx context.Context, tx pgx.Tx, r row, connected bool) error {
	raw, err := json.Marshal(statePayload(r, connected))
	if err != nil {
		return err
	}
	return bus.Enqueue(ctx, tx, bus.Event{ID: s.ids.Next(), Type: bus.VoiceState, GuildID: r.GuildID, ChannelID: r.ChannelID, Payload: raw})
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// voiceChannel exige canal de voz visível (404 se não vê; 422 se não é de voz).
func (s *Service) voiceChannel(ctx context.Context, user, channelID int64) (Channel, error) {
	ch, err := s.ch.VoiceAccess(ctx, user, channelID)
	if err != nil {
		return Channel{}, err
	}
	if !ch.Voice {
		return Channel{}, httpx.Validation("channel is not a voice channel")
	}
	return ch, nil
}

// Join põe o usuário no canal (saindo de outro, se estiver) e devolve a sessão de mídia. A permissão é checada agora
// e o token só sai depois disso. Entrar de novo no mesmo canal (reconexão) reemite o token sem evento.
func (s *Service) Join(ctx context.Context, user, channelID int64) (SessionView, error) {
	if !s.Enabled() {
		return SessionView{}, errNotSupported
	}
	ch, err := s.voiceChannel(ctx, user, channelID)
	if err != nil {
		return SessionView{}, err
	}
	if !ch.Connect {
		return SessionView{}, httpx.ErrMissingPermission
	}
	var view SessionView
	var name string
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, user); err != nil {
			return err
		}
		prev, err := currentRow(ctx, tx, user)
		if err != nil {
			return err
		}
		muted, camOn, sharing := false, false, false
		if prev != nil && prev.ChannelID == channelID {
			muted, camOn, sharing = prev.Muted, prev.CamOn, prev.Sharing
		}
		muted = muted || !ch.Speak // sem Speak, nasce (e fica) mutado
		cur, err := upsert(ctx, tx, user, ch.GuildID, channelID, muted, camOn, sharing)
		if err != nil {
			return err
		}
		name = cur.Name
		if prev != nil && prev.ChannelID != channelID {
			if err := s.emit(ctx, tx, *prev, false); err != nil {
				return err
			}
		}
		if prev == nil || prev.ChannelID != channelID {
			if err := s.emit(ctx, tx, cur, true); err != nil {
				return err
			}
		}
		rs, err := channelRows(ctx, tx, channelID)
		if err != nil {
			return err
		}
		view.Participants = participants(rs)
		return nil
	})
	if err != nil {
		return SessionView{}, err
	}
	tok, err := IssueToken(s.cfg.Secret, s.cfg.Now(), ids.Format(user), name, ids.Format(channelID), ch.Speak)
	if err != nil {
		return SessionView{}, err
	}
	view.Media.URL, view.Media.Token = s.cfg.SFUURL, tok
	view.ICEServers = s.cfg.ICEServers
	return view, nil
}

// Leave tira o usuário do canal. Idempotente: quem não está nele não gera erro nem evento.
func (s *Service) Leave(ctx context.Context, user, channelID int64) error {
	if !s.Enabled() {
		return errNotSupported
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, user); err != nil {
			return err
		}
		gone, err := deleteWhere(ctx, tx, `v.user_id = $1 AND v.channel_id = $2`, user, channelID)
		if err != nil {
			return err
		}
		return s.emitAll(ctx, tx, gone)
	})
}

func (s *Service) emitAll(ctx context.Context, tx pgx.Tx, rs []row) error {
	for _, r := range rs {
		if err := s.emit(ctx, tx, r, false); err != nil {
			return err
		}
	}
	return nil
}

// Participants lista quem está no canal. Exige ViewChannel (404 senão).
func (s *Service) Participants(ctx context.Context, user, channelID int64) ([]ParticipantView, error) {
	if !s.Enabled() {
		return nil, errNotSupported
	}
	if _, err := s.voiceChannel(ctx, user, channelID); err != nil {
		return nil, err
	}
	rs, err := channelRows(ctx, s.pool, channelID)
	if err != nil {
		return nil, err
	}
	return participants(rs), nil
}

// SetState muda mute/câmera/tela do próprio usuário (op `voice.state.set` do gateway). Sem Speak, muted fica true.
// Sem estado de voz, ou sem mudança, não faz nada (só renova o heartbeat).
func (s *Service) SetState(ctx context.Context, user int64, muted, camOn, sharing bool) error {
	cur, err := currentRow(ctx, s.pool, user)
	if err != nil || cur == nil {
		return err
	}
	ch, err := s.ch.VoiceAccess(ctx, user, cur.ChannelID)
	if err != nil {
		return nil // perdeu a visão do canal: o estado é varrido, não alterado
	}
	muted = muted || !ch.Speak
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, user); err != nil {
			return err
		}
		prev, err := currentRow(ctx, tx, user)
		if err != nil || prev == nil || prev.ChannelID != cur.ChannelID {
			return err
		}
		next, err := upsert(ctx, tx, user, prev.GuildID, prev.ChannelID, muted, camOn, sharing)
		if err != nil {
			return err
		}
		if prev.Muted == muted && prev.CamOn == camOn && prev.Sharing == sharing {
			return nil
		}
		return s.emit(ctx, tx, next, true)
	})
}

// Touch renova o heartbeat do usuário (chamado a cada heartbeat do gateway). Não faz nada se ele não está em voz.
func (s *Service) Touch(ctx context.Context, user int64) {
	if _, err := s.pool.Exec(ctx, `UPDATE voice_states SET updated_at = now() WHERE user_id = $1`, user); err != nil {
		slog.Warn("voice: touch", "user", user, "err", err)
	}
}

// RemoveMember tira da voz quem saiu, foi expulso ou banido da guild, emitindo o voice.state de saída.
func (s *Service) RemoveMember(ctx context.Context, guildID, user int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, user); err != nil {
			return err
		}
		gone, err := deleteWhere(ctx, tx, `v.user_id = $1 AND v.guild_id = $2`, user, guildID)
		if err != nil {
			return err
		}
		return s.emitAll(ctx, tx, gone)
	})
}

// Sweep apaga os estados sem heartbeat há mais de TTL e os de quem já não é membro da guild (rede de segurança
// para um member.leave perdido), emitindo a saída. Idempotente; qualquer réplica pode rodar.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		gone, err := deleteWhere(ctx, tx, `v.updated_at < now() - make_interval(secs => $1)
			OR NOT EXISTS (SELECT 1 FROM members m WHERE m.guild_id = v.guild_id AND m.user_id = v.user_id)`, s.cfg.TTL.Seconds())
		if err != nil {
			return err
		}
		n = len(gone)
		return s.emitAll(ctx, tx, gone)
	})
	return n, err
}

// Snapshot são os voice.state (connected=true) atuais das guilds do usuário, só dos canais que ele vê, e se o próprio
// usuário está em voz (mesmo num canal que já não vê). Vai no `ready`. Custa uma consulta aos estados e uma lista de
// canais visíveis por guild que tenha alguém em voz (antes eram três consultas por canal de voz).
func (s *Service) Snapshot(ctx context.Context, user int64) (states []json.RawMessage, inVoice bool, err error) {
	rs, err := guildRows(ctx, s.pool, user)
	if err != nil {
		return nil, false, err
	}
	visible := map[int64]map[int64]bool{} // guild -> canais visíveis
	out := []json.RawMessage{}
	for _, r := range rs {
		if r.UserID == user {
			inVoice = true
		}
		vis, seen := visible[r.GuildID]
		if !seen {
			vis, err = s.ch.VisibleChannels(ctx, user, r.GuildID)
			if err != nil {
				vis = nil // na dúvida, não entrega os estados dessa guild
			}
			visible[r.GuildID] = vis
		}
		if !vis[r.ChannelID] {
			continue
		}
		b, err := json.Marshal(statePayload(r, true))
		if err != nil {
			return nil, false, err
		}
		out = append(out, b)
	}
	return out, inVoice, nil
}

// Run liga a limpeza reativa (member.leave do bus) até ctx terminar. O handler do bus só enfileira; o trabalho
// roda numa goroutine própria. Se a fila encher, o evento é descartado e o Sweep recolhe o estado depois.
func (s *Service) Run(ctx context.Context, b bus.Bus) {
	work := make(chan bus.Event, 1024)
	cancel := b.Subscribe(func(_ context.Context, e bus.Event) {
		if e.Type != bus.MemberLeave {
			return
		}
		select {
		case work <- e:
		default:
			slog.Warn("voice: member.leave queue full; sweep will clean up", "guild", e.GuildID)
		}
	})
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-work:
			var p struct {
				UserID string `json:"userId"`
			}
			uid, err := int64(0), error(nil)
			if err = json.Unmarshal(e.Payload, &p); err == nil {
				uid, err = ids.Parse(p.UserID)
			}
			if err != nil {
				slog.Error("voice: bad member.leave payload", "err", err)
				continue
			}
			if err := s.RemoveMember(ctx, e.GuildID, uid); err != nil && ctx.Err() == nil {
				slog.Error("voice: remove member", "guild", e.GuildID, "user", uid, "err", err)
			}
		}
	}
}

// JobSweep é o kind do job periódico de limpeza.
const JobSweep = "voice.sweep"

// RegisterJobs registra a varredura periódica no worker de jobs. Sem voz configurada, não faz nada.
func (s *Service) RegisterJobs(w *jobs.Worker) {
	if !s.Enabled() {
		return
	}
	w.Handle(JobSweep, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.Sweep(ctx)
		return err
	})
	w.Every(JobSweep, sweepEvery)
}

const sweepEvery = 30 * time.Second
