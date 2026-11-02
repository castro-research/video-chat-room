// Package channels cuida de canais (texto, voz e categorias) e dos overrides de permissão deles.
// Não importa outros domínios: a guild entra pela interface Guilds, definida aqui.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

// Guilds é o que este módulo precisa do módulo de guilds. Subject devolve 404 para quem não é membro.
type Guilds interface {
	Subject(ctx context.Context, guildID, userID int64) (permissions.Subject, error)
	RolePosition(ctx context.Context, guildID, roleID int64) (int, error)
}

type Service struct {
	store  *store
	pool   *pgxpool.Pool
	ids    *ids.Generator
	guilds Guilds
}

func NewService(pool *pgxpool.Pool, gen *ids.Generator, guilds Guilds) *Service {
	return &Service{store: &store{db: pool}, pool: pool, ids: gen, guilds: guilds}
}

// inTx roda fn numa transação; eventos gravados com emit nela saem junto com a escrita (outbox).
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

func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ string, guildID, channelID int64, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return bus.Enqueue(ctx, tx, bus.Event{ID: s.ids.Next(), Type: typ, GuildID: guildID, ChannelID: channelID, Payload: raw})
}

// CreateDefaults cria o #geral de uma guild nova, dentro da transação de quem cria a guild.
func (s *Service) CreateDefaults(ctx context.Context, tx pgx.Tx, guildID int64) error {
	_, err := s.store.insert(ctx, tx, s.ids.Next(), guildID, nil, KindText, "geral")
	return err
}

func validName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > 100 {
		return "", httpx.Validation("name must have 1-100 characters")
	}
	return s, nil
}

// access carrega o canal e as permissões do usuário nele. Canal que o usuário não pode ver é 404.
func (s *Service) access(ctx context.Context, user, channelID int64) (Channel, permissions.Subject, permissions.Perm, error) {
	ch, err := s.store.channel(ctx, channelID)
	if errors.Is(err, errNotFound) {
		return Channel{}, permissions.Subject{}, 0, httpx.ErrNotFound
	}
	if err != nil {
		return Channel{}, permissions.Subject{}, 0, err
	}
	sub, err := s.guilds.Subject(ctx, ch.GuildID, user)
	if err != nil {
		return Channel{}, permissions.Subject{}, 0, err
	}
	ovs, err := s.store.overrides(ctx, channelID)
	if err != nil {
		return Channel{}, permissions.Subject{}, 0, err
	}
	perms := sub.InChannel(ovs)
	if !perms.Has(permissions.ViewChannel) {
		return Channel{}, permissions.Subject{}, 0, httpx.ErrNotFound
	}
	return ch, sub, perms, nil
}

// Permissions devolve o que o usuário pode no canal (0 se não pode ver). Para os módulos de mensagens e voz.
func (s *Service) Permissions(ctx context.Context, user, channelID int64) (permissions.Perm, error) {
	_, _, p, err := s.access(ctx, user, channelID)
	return p, err
}

type CreateInput struct {
	Name     string
	Kind     Kind
	ParentID *int64
}

func (s *Service) validParent(ctx context.Context, guildID int64, parent *int64) error {
	if parent == nil {
		return nil
	}
	p, err := s.store.channel(ctx, *parent)
	if errors.Is(err, errNotFound) || (err == nil && (p.GuildID != guildID || p.Kind != KindCategory)) {
		return httpx.Validation("parentId must be a category of this guild")
	}
	return err
}

func (s *Service) Create(ctx context.Context, user, guildID int64, in CreateInput) (Channel, error) {
	sub, err := s.guilds.Subject(ctx, guildID, user)
	if err != nil {
		return Channel{}, err
	}
	if !sub.Base().Has(permissions.ManageChannels) {
		return Channel{}, httpx.ErrMissingPermission
	}
	name, err := validName(in.Name)
	if err != nil {
		return Channel{}, err
	}
	switch in.Kind {
	case KindText, KindVoice:
	case KindCategory:
		if in.ParentID != nil {
			return Channel{}, httpx.Validation("a category cannot have a parent")
		}
	default:
		return Channel{}, httpx.Validation("kind must be text, voice or category")
	}
	if err := s.validParent(ctx, guildID, in.ParentID); err != nil {
		return Channel{}, err
	}
	var c Channel
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if c, err = s.store.insert(ctx, tx, s.ids.Next(), guildID, in.ParentID, in.Kind, name); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.ChannelCreate, guildID, c.ID, channelView(c))
	})
	return c, err
}

// List devolve os canais que o usuário pode ver, na ordem de exibição (por pai e posição).
func (s *Service) List(ctx context.Context, user, guildID int64) ([]Channel, error) {
	sub, err := s.guilds.Subject(ctx, guildID, user)
	if err != nil {
		return nil, err
	}
	all, err := s.store.channelsOf(ctx, guildID)
	if err != nil {
		return nil, err
	}
	ovs, err := s.store.overridesOfGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var out []Channel
	for _, c := range all {
		if sub.InChannel(ovs[c.ID]).Has(permissions.ViewChannel) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, user, channelID int64) (Channel, error) {
	ch, _, _, err := s.access(ctx, user, channelID)
	return ch, err
}

type UpdateInput struct {
	Name        *string
	ParentID    *int64
	ClearParent bool // parentId: null
	Position    *int
}

func (s *Service) Update(ctx context.Context, user, channelID int64, in UpdateInput) (Channel, error) {
	ch, _, perms, err := s.access(ctx, user, channelID)
	if err != nil {
		return Channel{}, err
	}
	if !perms.Has(permissions.ManageChannels) {
		return Channel{}, httpx.ErrMissingPermission
	}
	var name *string
	if in.Name != nil {
		n, err := validName(*in.Name)
		if err != nil {
			return Channel{}, err
		}
		name = &n
	}
	var parentChange **int64
	if in.ParentID != nil || in.ClearParent {
		if ch.Kind == KindCategory {
			return Channel{}, httpx.Validation("a category cannot have a parent")
		}
		if err := s.validParent(ctx, ch.GuildID, in.ParentID); err != nil {
			return Channel{}, err
		}
		if in.ParentID != nil {
			// mexer na categoria de destino também exige poder gerenciá-la
			if _, _, pp, err := s.access(ctx, user, *in.ParentID); err != nil || !pp.Has(permissions.ManageChannels) {
				return Channel{}, httpx.ErrMissingPermission
			}
		}
		p := in.ParentID
		parentChange = &p
	}
	if in.Position != nil && *in.Position < 0 {
		return Channel{}, httpx.Validation("position must be 0 or more")
	}
	var out Channel
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if out, err = s.store.update(ctx, tx, channelID, name, parentChange, in.Position); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.ChannelUpdate, out.GuildID, channelID, channelView(out))
	})
	if errors.Is(err, errNotFound) {
		return Channel{}, httpx.ErrNotFound
	}
	return out, err
}

func (s *Service) Delete(ctx context.Context, user, channelID int64) error {
	ch, _, perms, err := s.access(ctx, user, channelID)
	if err != nil {
		return err
	}
	if !perms.Has(permissions.ManageChannels) {
		return httpx.ErrMissingPermission
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.delete(ctx, tx, channelID); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.ChannelDelete, ch.GuildID, channelID,
			map[string]string{"id": ids.Format(channelID), "guildId": ids.Format(ch.GuildID)})
	})
}

// ---- overrides ----

func (s *Service) Overrides(ctx context.Context, user, channelID int64) ([]permissions.Override, error) {
	_, _, perms, err := s.access(ctx, user, channelID)
	if err != nil {
		return nil, err
	}
	if !perms.Has(permissions.ManageChannels) {
		return nil, httpx.ErrMissingPermission
	}
	return s.store.overrides(ctx, channelID)
}

// checkTarget valida o alvo do override e a hierarquia: papéis só abaixo do ator (@everyone sempre),
// membros só abaixo do ator (ou ele mesmo).
func (s *Service) checkTarget(ctx context.Context, guildID int64, actor permissions.Subject, kind permissions.TargetKind, target int64) error {
	switch kind {
	case permissions.TargetRole:
		pos, err := s.guilds.RolePosition(ctx, guildID, target)
		if err != nil {
			return err
		}
		if target != guildID && !permissions.CanManageRole(actor, pos) {
			return httpx.ErrHierarchy
		}
	case permissions.TargetMember:
		t, err := s.guilds.Subject(ctx, guildID, target)
		if err != nil {
			return err
		}
		if target != actor.UserID && !permissions.CanManageMember(actor, t) {
			return httpx.ErrHierarchy
		}
	default:
		return httpx.Validation("kind must be role or member")
	}
	return nil
}

func (s *Service) SetOverride(ctx context.Context, user, channelID int64, o permissions.Override) (permissions.Override, error) {
	ch, actor, perms, err := s.access(ctx, user, channelID)
	if err != nil {
		return o, err
	}
	if !perms.Has(permissions.ManageChannels) {
		return o, httpx.ErrMissingPermission
	}
	if !o.Allow.Valid() || !o.Deny.Valid() || (o.Allow | o.Deny).Has(permissions.Administrator) {
		return o, httpx.Validation("unknown permission bits, or Administrator in an override")
	}
	if o.Allow&o.Deny != 0 {
		return o, httpx.Validation("a permission cannot be both allowed and denied")
	}
	if err := s.checkTarget(ctx, ch.GuildID, actor, o.Kind, o.TargetID); err != nil {
		return o, err
	}
	if !permissions.CanGrant(perms, o.Allow) {
		return o, httpx.ErrMissingPermission
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if o.Allow == 0 && o.Deny == 0 {
			err = s.store.deleteOverride(ctx, tx, channelID, o.Kind, o.TargetID)
		} else {
			err = s.store.setOverride(ctx, tx, channelID, o)
		}
		if err != nil {
			return err
		}
		return s.emitOverride(ctx, tx, ch, o)
	})
	return o, err
}

func (s *Service) DeleteOverride(ctx context.Context, user, channelID int64, kind permissions.TargetKind, target int64) error {
	ch, actor, perms, err := s.access(ctx, user, channelID)
	if err != nil {
		return err
	}
	if !perms.Has(permissions.ManageChannels) {
		return httpx.ErrMissingPermission
	}
	// o alvo pode já ter saído da guild; só exigimos hierarquia quando ainda existe
	if err := s.checkTarget(ctx, ch.GuildID, actor, kind, target); err != nil && !isNotFound(err) {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.deleteOverride(ctx, tx, channelID, kind, target); err != nil {
			return err
		}
		return s.emitOverride(ctx, tx, ch, permissions.Override{Kind: kind, TargetID: target})
	})
}

// emitOverride avisa que as permissões do canal mudaram: o gateway invalida o cache e reroteia dali em diante.
func (s *Service) emitOverride(ctx context.Context, tx pgx.Tx, ch Channel, o permissions.Override) error {
	return s.emit(ctx, tx, bus.ChannelPermissions, ch.GuildID, ch.ID, map[string]any{
		"channelId": ids.Format(ch.ID), "guildId": ids.Format(ch.GuildID), "override": overrideView(o)})
}

func isNotFound(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Status == 404
}

// Access devolve o canal e o que o usuário pode nele, para o módulo de mensagens. Canal invisível é 404.
func (s *Service) Access(ctx context.Context, user, channelID int64) (Channel, permissions.Perm, error) {
	ch, _, p, err := s.access(ctx, user, channelID)
	return ch, p, err
}
