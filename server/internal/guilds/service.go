// Package guilds cuida de guilds, membros, papéis, convites e bans. Não importa outros domínios:
// o que precisa de fora entra por interfaces pequenas definidas aqui.
package guilds

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/bus"
	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

// DefaultChannels cria os canais iniciais de uma guild nova (#geral) dentro da transação da criação.
// Quem implementa é o módulo de canais; a guild só conhece este contrato.
type DefaultChannels interface {
	CreateDefaults(ctx context.Context, tx pgx.Tx, guildID int64) error
}

type Service struct {
	store    *store
	ids      *ids.Generator
	pool     *pgxpool.Pool
	channels DefaultChannels
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, gen *ids.Generator, channels DefaultChannels) *Service {
	return &Service{store: &store{db: pool}, ids: gen, pool: pool, channels: channels, now: time.Now}
}

// Subject devolve o membro e os papéis dele, para outros módulos resolverem permissões.
// Quem não é membro (ou a guild não existe) recebe 404, sem revelar a existência da guild.
func (s *Service) Subject(ctx context.Context, guildID, userID int64) (permissions.Subject, error) {
	return s.subject(ctx, guildID, userID)
}

// RolePosition devolve a posição de um papel da guild (para hierarquia em overrides de canal).
func (s *Service) RolePosition(ctx context.Context, guildID, roleID int64) (int, error) {
	r, err := s.store.role(ctx, guildID, roleID)
	if errors.Is(err, errNotFound) {
		return 0, httpx.ErrNotFound
	}
	return r.Position, err
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

// emit grava um evento da guild no outbox, na transação tx. Os ids no payload seguem o formato da API (string).
func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ string, guildID int64, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return bus.Enqueue(ctx, tx, bus.Event{ID: s.ids.Next(), Type: typ, GuildID: guildID, Payload: raw})
}

func (s *Service) emitMemberLeave(ctx context.Context, tx pgx.Tx, guildID, userID int64, reason string) error {
	return s.emit(ctx, tx, bus.MemberLeave, guildID, map[string]string{
		"guildId": ids.Format(guildID), "userId": ids.Format(userID), "reason": reason})
}

func (s *Service) emitMemberUpdate(ctx context.Context, tx pgx.Tx, typ string, guildID, userID int64) error {
	m, err := s.store.member(ctx, tx, guildID, userID)
	if err != nil {
		return err
	}
	return s.emit(ctx, tx, typ, guildID, map[string]any{
		"guildId": ids.Format(guildID), "userId": ids.Format(userID), "member": memberView(m)})
}

func (s *Service) subject(ctx context.Context, guildID, userID int64) (permissions.Subject, error) {
	sub, err := s.store.subject(ctx, s.pool, guildID, userID)
	if errors.Is(err, errNotMember) {
		return permissions.Subject{}, httpx.ErrNotFound
	}
	return sub, err
}

func need(sub permissions.Subject, p permissions.Perm) error {
	if !sub.Base().Has(p) {
		return httpx.ErrMissingPermission
	}
	return nil
}

var errOwnerOnly = httpx.NewError(http.StatusForbidden, "forbidden", "only the owner can do this")

func validName(s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > max {
		return "", httpx.Validation("name must have 1-" + ids.Format(int64(max)) + " characters")
	}
	return s, nil
}

// ---- guilds ----

func (s *Service) Create(ctx context.Context, user int64, name string) (Guild, error) {
	name, err := validName(name, 100)
	if err != nil {
		return Guild{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Guild{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	g, err := s.store.insertGuild(ctx, tx, s.ids.Next(), name, user)
	if err != nil {
		return Guild{}, err
	}
	if err := s.channels.CreateDefaults(ctx, tx, g.ID); err != nil {
		return Guild{}, err
	}
	if err := s.emit(ctx, tx, bus.GuildCreate, g.ID, map[string]any{"userId": ids.Format(user), "guild": guildView(g)}); err != nil {
		return Guild{}, err
	}
	return g, tx.Commit(ctx)
}

func (s *Service) Mine(ctx context.Context, user int64) ([]Guild, error) {
	return s.store.guildsOf(ctx, user)
}

func (s *Service) Get(ctx context.Context, user, guildID int64) (Guild, error) {
	if _, err := s.subject(ctx, guildID, user); err != nil {
		return Guild{}, err
	}
	return s.store.guild(ctx, guildID)
}

func (s *Service) Delete(ctx context.Context, user, guildID int64) error {
	sub, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if !sub.IsOwner {
		return errOwnerOnly
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.deleteGuild(ctx, tx, guildID); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.GuildDelete, guildID, map[string]string{"id": ids.Format(guildID)})
	})
}

func (s *Service) TransferOwnership(ctx context.Context, user, guildID, newOwner int64) error {
	sub, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if !sub.IsOwner {
		return errOwnerOnly
	}
	if newOwner == user {
		return httpx.Validation("you already own this guild")
	}
	if _, err := s.subject(ctx, guildID, newOwner); err != nil {
		return httpx.Validation("the new owner must be a member")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.setOwner(ctx, tx, guildID, newOwner); err != nil {
			return err
		}
		g, err := s.store.guildIn(ctx, tx, guildID)
		if err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.GuildUpdate, guildID, guildView(g))
	})
}

// ---- membros ----

func (s *Service) Members(ctx context.Context, user, guildID, after int64, limit int) ([]Member, int, error) {
	if _, err := s.subject(ctx, guildID, user); err != nil {
		return nil, 0, err
	}
	return s.store.members(ctx, guildID, after, limit)
}

func (s *Service) Leave(ctx context.Context, user, guildID int64) error {
	sub, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if sub.IsOwner {
		return httpx.NewError(http.StatusConflict, "owner_cannot_leave", "transfer ownership or delete the guild first")
	}
	return s.removeMember(ctx, guildID, user, "leave")
}

// target carrega o alvo de uma ação de moderação. allowOutsider: o alvo pode não ser membro (ban).
func (s *Service) target(ctx context.Context, guildID, user, targetID int64, allowOutsider bool) (permissions.Subject, error) {
	if targetID == user {
		return permissions.Subject{}, httpx.Validation("you cannot do this to yourself")
	}
	t, err := s.store.subject(ctx, s.pool, guildID, targetID)
	if errors.Is(err, errNotMember) {
		if allowOutsider {
			return permissions.Subject{UserID: targetID}, nil
		}
		return permissions.Subject{}, httpx.ErrNotFound
	}
	return t, err
}

func (s *Service) Kick(ctx context.Context, user, guildID, targetID int64) error {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if err := need(actor, permissions.KickMembers); err != nil {
		return err
	}
	t, err := s.target(ctx, guildID, user, targetID, false)
	if err != nil {
		return err
	}
	if !permissions.CanManageMember(actor, t) {
		return httpx.ErrHierarchy
	}
	return s.removeMember(ctx, guildID, targetID, "kick")
}

func (s *Service) removeMember(ctx context.Context, guildID, userID int64, reason string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.removeMember(ctx, tx, guildID, userID); err != nil {
			return err
		}
		return s.emitMemberLeave(ctx, tx, guildID, userID, reason)
	})
}

func (s *Service) Ban(ctx context.Context, user, guildID, targetID int64, reason string) error {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if err := need(actor, permissions.BanMembers); err != nil {
		return err
	}
	if utf8.RuneCountInString(reason) > 512 {
		return httpx.Validation("reason must have at most 512 characters")
	}
	t, err := s.target(ctx, guildID, user, targetID, true)
	if err != nil {
		return err
	}
	if !permissions.CanManageMember(actor, t) {
		return httpx.ErrHierarchy
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		wasMember, err := s.store.ban(ctx, tx, guildID, targetID, user, reason)
		if err != nil || !wasMember {
			return err
		}
		return s.emitMemberLeave(ctx, tx, guildID, targetID, "ban")
	})
	if isFKViolation(err) {
		return httpx.ErrNotFound // usuário inexistente
	}
	return err
}

func (s *Service) Unban(ctx context.Context, user, guildID, targetID int64) error {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if err := need(actor, permissions.BanMembers); err != nil {
		return err
	}
	err = s.store.unban(ctx, guildID, targetID)
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	return err
}

func (s *Service) Bans(ctx context.Context, user, guildID int64) ([]Ban, error) {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return nil, err
	}
	if err := need(actor, permissions.BanMembers); err != nil {
		return nil, err
	}
	return s.store.bans(ctx, guildID)
}

// ---- papéis ----

func (s *Service) Roles(ctx context.Context, user, guildID int64) ([]Role, error) {
	if _, err := s.subject(ctx, guildID, user); err != nil {
		return nil, err
	}
	return s.store.roles(ctx, s.pool, guildID, false)
}

type RoleInput struct {
	Name        *string `json:"name"`
	Permissions *uint64 `json:"permissions"`
	Position    *int    `json:"position"`
}

func (in RoleInput) perms() (*permissions.Perm, error) {
	if in.Permissions == nil {
		return nil, nil
	}
	p := permissions.Perm(*in.Permissions)
	if !p.Valid() {
		return nil, httpx.Validation("unknown permission bits")
	}
	return &p, nil
}

func (s *Service) CreateRole(ctx context.Context, user, guildID int64, in RoleInput) (Role, error) {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return Role{}, err
	}
	if err := need(actor, permissions.ManageRoles); err != nil {
		return Role{}, err
	}
	if in.Name == nil {
		return Role{}, httpx.Validation("name is required")
	}
	name, err := validName(*in.Name, 64)
	if err != nil {
		return Role{}, err
	}
	perms, err := in.perms()
	if err != nil {
		return Role{}, err
	}
	var p permissions.Perm
	if perms != nil {
		p = *perms
	}
	if !permissions.CanGrant(actor.Base(), p) {
		return Role{}, httpx.ErrMissingPermission
	}
	var role Role
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if role, err = s.store.insertRole(ctx, tx, s.ids.Next(), guildID, name, p); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.RoleCreate, guildID, map[string]any{"guildId": ids.Format(guildID), "role": roleView(role)})
	})
	return role, err
}

func (s *Service) UpdateRole(ctx context.Context, user, guildID, roleID int64, in RoleInput) (Role, error) {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return Role{}, err
	}
	if err := need(actor, permissions.ManageRoles); err != nil {
		return Role{}, err
	}
	perms, err := in.perms()
	if err != nil {
		return Role{}, err
	}
	var name *string
	if in.Name != nil {
		n, err := validName(*in.Name, 64)
		if err != nil {
			return Role{}, err
		}
		name = &n
	}
	if in.Position != nil && *in.Position < 1 {
		return Role{}, httpx.Validation("position must be at least 1")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, err
	}
	defer tx.Rollback(ctx)                              //nolint:errcheck
	roles, err := s.store.roles(ctx, tx, guildID, true) // trava os papéis da guild
	if err != nil {
		return Role{}, err
	}
	var role *Role
	for i := range roles {
		if roles[i].ID == roleID {
			role = &roles[i]
		}
	}
	if role == nil {
		return Role{}, httpx.ErrNotFound
	}
	if !permissions.CanManageRole(actor, role.Position) {
		return Role{}, httpx.ErrHierarchy
	}
	if role.Everyone && (name != nil || in.Position != nil) {
		return Role{}, httpx.Validation("@everyone cannot be renamed or moved")
	}
	if perms != nil && !permissions.CanGrant(actor.Base(), *perms&^role.Permissions) {
		return Role{}, httpx.ErrMissingPermission
	}

	updated, err := s.store.updateRole(ctx, tx, guildID, roleID, name, perms)
	if err != nil {
		return Role{}, err
	}
	if in.Position != nil {
		var order []int64
		for _, r := range roles {
			if !r.Everyone && r.ID != roleID {
				order = append(order, r.ID)
			}
		}
		idx := min(*in.Position-1, len(order))
		order = append(order[:idx], append([]int64{roleID}, order[idx:]...)...)
		// depois de mover, o papel continua abaixo do topo do ator
		if !actor.IsOwner && idx+1 >= positionOf(order, actor.TopRoleID()) {
			return Role{}, httpx.ErrHierarchy
		}
		if err := s.store.setRolePositions(ctx, tx, order); err != nil {
			return Role{}, err
		}
		updated.Position = idx + 1
	}
	if err := s.emit(ctx, tx, bus.RoleUpdate, guildID, map[string]any{"guildId": ids.Format(guildID), "role": roleView(updated)}); err != nil {
		return Role{}, err
	}
	return updated, tx.Commit(ctx)
}

// positionOf devolve a posição (1-based) de id em order, ou um valor enorme se não estiver (nada a proteger).
func positionOf(order []int64, id int64) int {
	for i, v := range order {
		if v == id {
			return i + 1
		}
	}
	return 1 << 30
}

func (s *Service) DeleteRole(ctx context.Context, user, guildID, roleID int64) error {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if err := need(actor, permissions.ManageRoles); err != nil {
		return err
	}
	role, err := s.store.role(ctx, guildID, roleID)
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if role.Everyone {
		return httpx.Validation("@everyone cannot be deleted")
	}
	if !permissions.CanManageRole(actor, role.Position) {
		return httpx.ErrHierarchy
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.deleteRole(ctx, tx, guildID, roleID); err != nil {
			return err
		}
		return s.emit(ctx, tx, bus.RoleDelete, guildID, map[string]string{"guildId": ids.Format(guildID), "roleId": ids.Format(roleID)})
	})
}

// SetMemberRole atribui (grant) ou tira um papel de um membro.
func (s *Service) SetMemberRole(ctx context.Context, user, guildID, targetID, roleID int64, grant bool) error {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return err
	}
	if err := need(actor, permissions.ManageRoles); err != nil {
		return err
	}
	role, err := s.store.role(ctx, guildID, roleID)
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if role.Everyone {
		return httpx.Validation("@everyone cannot be assigned")
	}
	if !permissions.CanManageRole(actor, role.Position) {
		return httpx.ErrHierarchy
	}
	t, err := s.store.subject(ctx, s.pool, guildID, targetID)
	if errors.Is(err, errNotMember) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if targetID != user && !permissions.CanManageMember(actor, t) {
		return httpx.ErrHierarchy
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if grant {
			err = s.store.addMemberRole(ctx, tx, guildID, targetID, roleID)
		} else {
			err = s.store.removeMemberRole(ctx, tx, guildID, targetID, roleID)
		}
		if err != nil {
			return err
		}
		return s.emitMemberUpdate(ctx, tx, bus.MemberUpdate, guildID, targetID)
	})
}

// ---- convites ----

type InviteInput struct {
	MaxUses   *int `json:"maxUses"`
	ExpiresIn *int `json:"expiresIn"` // segundos
}

const codeAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b), nil
}

func (s *Service) CreateInvite(ctx context.Context, user, guildID int64, in InviteInput) (Invite, error) {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return Invite{}, err
	}
	if err := need(actor, permissions.CreateInvite); err != nil {
		return Invite{}, err
	}
	if in.MaxUses != nil && (*in.MaxUses < 1 || *in.MaxUses > 1_000_000) {
		return Invite{}, httpx.Validation("maxUses must be between 1 and 1000000")
	}
	inv := Invite{GuildID: guildID, CreatorID: user, MaxUses: in.MaxUses}
	if in.ExpiresIn != nil {
		if *in.ExpiresIn < 1 || *in.ExpiresIn > 365*24*3600 {
			return Invite{}, httpx.Validation("expiresIn must be between 1 second and 1 year")
		}
		t := s.now().Add(time.Duration(*in.ExpiresIn) * time.Second)
		inv.ExpiresAt = &t
	}
	if inv.Code, err = newCode(); err != nil {
		return Invite{}, err
	}
	return s.store.insertInvite(ctx, inv)
}

func (s *Service) Invites(ctx context.Context, user, guildID int64) ([]Invite, error) {
	actor, err := s.subject(ctx, guildID, user)
	if err != nil {
		return nil, err
	}
	if err := need(actor, permissions.CreateInvite); err != nil {
		return nil, err
	}
	return s.store.invitesOf(ctx, guildID)
}

// RevokeInvite: o criador ou um Administrator.
func (s *Service) RevokeInvite(ctx context.Context, user int64, code string) error {
	inv, err := s.store.invite(ctx, code)
	if errors.Is(err, errNotFound) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	actor, err := s.subject(ctx, inv.GuildID, user)
	if err != nil {
		return err
	}
	if inv.CreatorID != user && !actor.Base().Has(permissions.Administrator) {
		return httpx.ErrMissingPermission
	}
	return s.store.deleteInvite(ctx, code)
}

var (
	errInvalidInvite = httpx.NewError(http.StatusNotFound, "invalid_invite", "invite not found")
	errInviteExpired = httpx.NewError(http.StatusGone, "invite_expired", "this invite has expired or reached its limit")
	errBannedHTTP    = httpx.NewError(http.StatusForbidden, "banned", "you are banned from this guild")
)

// Preview mostra a guild de um convite sem entrar.
func (s *Service) Preview(ctx context.Context, code string) (Invite, Guild, error) {
	inv, err := s.store.invite(ctx, code)
	if errors.Is(err, errNotFound) {
		return Invite{}, Guild{}, errInvalidInvite
	}
	if err != nil {
		return Invite{}, Guild{}, err
	}
	if (inv.ExpiresAt != nil && !s.now().Before(*inv.ExpiresAt)) || (inv.MaxUses != nil && inv.Uses >= *inv.MaxUses) {
		return Invite{}, Guild{}, errInviteExpired
	}
	g, err := s.store.guild(ctx, inv.GuildID)
	return inv, g, err
}

// Join entra na guild pelo código. Já ser membro é idempotente e não gasta uso.
func (s *Service) Join(ctx context.Context, user int64, code string) (Guild, error) {
	var g Guild
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var joined bool
		var err error
		if g, joined, err = s.store.join(ctx, tx, code, user, s.now()); err != nil || !joined {
			return err
		}
		return s.emitMemberUpdate(ctx, tx, bus.MemberJoin, g.ID, user)
	})
	switch {
	case errors.Is(err, errNotFound):
		return Guild{}, errInvalidInvite
	case errors.Is(err, errInviteGone):
		return Guild{}, errInviteExpired
	case errors.Is(err, errBanned):
		return Guild{}, errBannedHTTP
	}
	return g, err
}
