// Package messages cuida de mensagens, reações e estado de leitura dos canais de texto.
// Não importa guilds nem channels: o acesso ao canal entra pela interface Perms, definida aqui.
package messages

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

const (
	MaxTextRunes      = 4000
	MaxNonceLen       = 64
	MaxEmojiBytes     = 64
	MaxEmojisPerMsg   = 20
	DefaultPageLimit  = 50
	MaxPageLimit      = 100
	pgForeignKeyError = "23503"
)

// Access é o que o usuário pode num canal. Text diz se o canal aceita mensagens (só canais de texto).
type Access struct {
	Perms permissions.Perm
	Text  bool
}

// Perms é o que este módulo precisa dos módulos de guilds/canais. ChannelAccess devolve 404
// (httpx.ErrNotFound) para quem não é membro ou não vê o canal; VisibleTextChannels, para quem não é membro da guild.
type Perms interface {
	ChannelAccess(ctx context.Context, userID, channelID int64) (Access, error)
	VisibleTextChannels(ctx context.Context, userID, guildID int64) ([]int64, error)
}

type Service struct {
	store *store
	pool  *pgxpool.Pool
	ids   *ids.Generator
	perms Perms

	onRecord func(Event) // só para testes: observa os eventos gravados
}

func NewService(pool *pgxpool.Pool, gen *ids.Generator, perms Perms) *Service {
	return &Service{store: &store{db: pool}, pool: pool, ids: gen, perms: perms}
}

// Event é o que uma escrita produz para o outbox.
type Event struct {
	Kind      string // message.create, message.update, message.delete, reaction.add, reaction.remove
	ChannelID int64
	MessageID int64
	UserID    int64
}

// record é o ponto onde o Plano 4 grava o evento na tabela outbox, DENTRO da mesma transação da escrita.
// Hoje não faz nada.
func (s *Service) record(_ context.Context, _ pgx.Tx, e Event) error {
	if s.onRecord != nil {
		s.onRecord(e)
	}
	return nil
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// access exige ViewChannel (via Perms), que o canal seja de texto e, se want != 0, a permissão want.
func (s *Service) access(ctx context.Context, user, channel int64, want permissions.Perm) error {
	a, err := s.perms.ChannelAccess(ctx, user, channel)
	if err != nil {
		return err
	}
	if !a.Perms.Has(permissions.ViewChannel) {
		return httpx.ErrNotFound
	}
	if !a.Text {
		return httpx.Validation("this channel does not support messages")
	}
	if !a.Perms.Has(want) {
		return httpx.ErrMissingPermission
	}
	return nil
}

func validText(s string) error {
	if strings.TrimSpace(s) == "" {
		return httpx.Validation("text must not be empty")
	}
	if utf8.RuneCountInString(s) > MaxTextRunes {
		return httpx.Validation("text must have at most 4000 characters")
	}
	return nil
}

func validEmoji(s string) error {
	if s == "" || len(s) > MaxEmojiBytes || !utf8.ValidString(s) {
		return httpx.Validation("emoji is invalid")
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return httpx.Validation("emoji is invalid")
		}
	}
	return nil
}

type SendInput struct {
	Text      string
	ReplyToID *int64
	Nonce     string
}

func (s *Service) Send(ctx context.Context, user, channel int64, in SendInput) (Message, error) {
	if err := s.access(ctx, user, channel, permissions.SendMessages); err != nil {
		return Message{}, err
	}
	if err := validText(in.Text); err != nil {
		return Message{}, err
	}
	if len(in.Nonce) > MaxNonceLen {
		return Message{}, httpx.Validation("nonce must have at most 64 characters")
	}
	var out Message
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.lockChannel(ctx, tx, channel); err != nil {
			return err
		}
		if in.ReplyToID != nil {
			if _, err := s.store.message(ctx, tx, channel, *in.ReplyToID); errors.Is(err, errNotFound) {
				return httpx.Validation("replyToId must be a message of this channel")
			} else if err != nil {
				return err
			}
		}
		id := s.ids.Next() // com o lock do canal, a ordem de commit é a ordem dos ids
		if err := s.store.insert(ctx, tx, id, channel, user, in.Text, in.ReplyToID); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == pgForeignKeyError {
				return httpx.Validation("replyToId must be a message of this channel")
			}
			return err
		}
		m, err := s.store.message(ctx, tx, channel, id)
		if err != nil {
			return err
		}
		out = m
		return s.record(ctx, tx, Event{Kind: "message.create", ChannelID: channel, MessageID: id, UserID: user})
	})
	if err != nil {
		return Message{}, err
	}
	out.Nonce = in.Nonce
	return out, nil
}

type Direction int

const (
	Latest Direction = iota
	Before
	After
	Around
)

type ListInput struct {
	Dir    Direction
	Cursor int64
	Limit  int
}

type Page struct {
	Items    []Message
	HasMore  bool // há mensagens mais antigas que o primeiro item
	HasNewer bool // há mensagens mais novas que o último item
}

func (s *Service) List(ctx context.Context, user, channel int64, in ListInput) (Page, error) {
	if err := s.access(ctx, user, channel, 0); err != nil {
		return Page{}, err
	}
	if in.Limit < 1 || in.Limit > MaxPageLimit {
		in.Limit = DefaultPageLimit
	}
	mode := map[Direction]pageMode{Latest: pageLatest, Before: pageBefore, After: pageAfter, Around: pageAround}[in.Dir]
	ms, err := s.store.page(ctx, channel, mode, in.Cursor, in.Limit)
	if err != nil {
		return Page{}, err
	}
	if err := s.store.attachReactions(ctx, s.pool, ms, user); err != nil {
		return Page{}, err
	}
	p := Page{Items: ms}
	if len(ms) > 0 {
		if p.HasMore, err = s.store.hasOlder(ctx, s.pool, channel, ms[0].ID); err != nil {
			return Page{}, err
		}
		if p.HasNewer, err = s.store.hasNewer(ctx, s.pool, channel, ms[len(ms)-1].ID); err != nil {
			return Page{}, err
		}
	} else if in.Dir == Before {
		p.HasNewer = true
	}
	return p, nil
}

// withReactions devolve a mensagem já com as reações, vista por viewer.
func (s *Service) withReactions(ctx context.Context, m Message, viewer int64) (Message, error) {
	ms := []Message{m}
	if err := s.store.attachReactions(ctx, s.pool, ms, viewer); err != nil {
		return Message{}, err
	}
	return ms[0], nil
}

// Edit: só o autor edita a própria mensagem.
func (s *Service) Edit(ctx context.Context, user, channel, id int64, text string) (Message, error) {
	if err := s.access(ctx, user, channel, 0); err != nil {
		return Message{}, err
	}
	if err := validText(text); err != nil {
		return Message{}, err
	}
	var out Message
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.lockMessage(ctx, tx, channel, id); err != nil {
			return err
		}
		m, err := s.store.message(ctx, tx, channel, id)
		if err != nil {
			return err
		}
		if m.Author.ID != user {
			return httpx.ErrForbidden
		}
		if err := s.store.update(ctx, tx, channel, id, text); err != nil {
			return err
		}
		if out, err = s.store.message(ctx, tx, channel, id); err != nil {
			return err
		}
		return s.record(ctx, tx, Event{Kind: "message.update", ChannelID: channel, MessageID: id, UserID: user})
	})
	if errors.Is(err, errNotFound) {
		return Message{}, httpx.ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.withReactions(ctx, out, user)
}

// Delete: o autor apaga a própria; ManageMessages apaga a de qualquer um.
func (s *Service) Delete(ctx context.Context, user, channel, id int64) error {
	a, err := s.perms.ChannelAccess(ctx, user, channel)
	if err != nil {
		return err
	}
	if !a.Perms.Has(permissions.ViewChannel) || !a.Text {
		return httpx.ErrNotFound
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.lockMessage(ctx, tx, channel, id); err != nil {
			return err
		}
		m, err := s.store.message(ctx, tx, channel, id)
		if err != nil {
			return err
		}
		if m.Author.ID != user && !a.Perms.Has(permissions.ManageMessages) {
			return httpx.ErrMissingPermission
		}
		if err := s.store.delete(ctx, tx, channel, id); err != nil {
			return err
		}
		return s.record(ctx, tx, Event{Kind: "message.delete", ChannelID: channel, MessageID: id, UserID: user})
	})
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	return err
}

// AddReaction é idempotente. Reagir exige SendMessages (canal somente leitura não aceita reações novas).
func (s *Service) AddReaction(ctx context.Context, user, channel, id int64, emoji string) error {
	if err := s.access(ctx, user, channel, permissions.SendMessages); err != nil {
		return err
	}
	if err := validEmoji(emoji); err != nil {
		return err
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.lockMessage(ctx, tx, channel, id); err != nil {
			return err
		}
		n, exists, err := s.store.distinctEmojis(ctx, tx, id, emoji)
		if err != nil {
			return err
		}
		if !exists && n >= MaxEmojisPerMsg {
			return httpx.Validation("a message takes at most 20 different emojis")
		}
		if err := s.store.addReaction(ctx, tx, id, user, emoji); err != nil {
			return err
		}
		return s.record(ctx, tx, Event{Kind: "reaction.add", ChannelID: channel, MessageID: id, UserID: user})
	})
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	return err
}

// RemoveReaction remove a reação de target (0 = o próprio usuário). Remover a de outro exige ManageMessages.
// Idempotente: reação que não existe também dá certo.
func (s *Service) RemoveReaction(ctx context.Context, user, channel, id int64, emoji string, target int64) error {
	a, err := s.perms.ChannelAccess(ctx, user, channel)
	if err != nil {
		return err
	}
	if !a.Perms.Has(permissions.ViewChannel) || !a.Text {
		return httpx.ErrNotFound
	}
	if err := validEmoji(emoji); err != nil {
		return err
	}
	if target == 0 {
		target = user
	}
	if target != user && !a.Perms.Has(permissions.ManageMessages) {
		return httpx.ErrMissingPermission
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.lockMessage(ctx, tx, channel, id); err != nil {
			return err
		}
		if err := s.store.removeReaction(ctx, tx, id, target, emoji); err != nil {
			return err
		}
		return s.record(ctx, tx, Event{Kind: "reaction.remove", ChannelID: channel, MessageID: id, UserID: target})
	})
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	return err
}

// Ack marca o canal como lido até a mensagem (que precisa existir no canal). Nunca recua.
func (s *Service) Ack(ctx context.Context, user, channel, id int64) error {
	if err := s.access(ctx, user, channel, 0); err != nil {
		return err
	}
	ok, err := s.store.messageExists(ctx, channel, id)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Validation("messageId must be a message of this channel")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error { return s.store.ack(ctx, tx, user, channel, id) })
}

func (s *Service) ChannelReadState(ctx context.Context, user, channel int64) (ReadState, error) {
	if err := s.access(ctx, user, channel, 0); err != nil {
		return ReadState{}, err
	}
	rs, err := s.store.readStates(ctx, user, []int64{channel})
	if err != nil || len(rs) == 0 {
		return ReadState{}, err
	}
	return rs[0], nil
}

// GuildReadStates devolve o estado de leitura dos canais de texto que o usuário vê na guild.
func (s *Service) GuildReadStates(ctx context.Context, user, guild int64) ([]ReadState, error) {
	chs, err := s.perms.VisibleTextChannels(ctx, user, guild)
	if err != nil {
		return nil, err
	}
	return s.store.readStates(ctx, user, chs)
}
