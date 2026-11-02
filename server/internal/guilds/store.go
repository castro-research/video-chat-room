package guilds

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/permissions"
)

var (
	errNotFound   = errors.New("guilds: not found")
	errNotMember  = errors.New("guilds: not a member")
	errInviteGone = errors.New("guilds: invite expired")
)

// querier é o que pool e tx têm em comum.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type store struct{ db *pgxpool.Pool }

type Guild struct {
	ID        int64
	Name      string
	OwnerID   int64
	CreatedAt time.Time
}

type Role struct {
	ID          int64
	GuildID     int64
	Name        string
	Permissions permissions.Perm
	Position    int
	Everyone    bool
}

type Member struct {
	UserID   int64
	Username string
	Name     string
	Subtitle string
	Hue      *float64
	JoinedAt time.Time
	RoleIDs  []int64
}

type Invite struct {
	Code      string
	GuildID   int64
	CreatorID int64
	MaxUses   *int
	Uses      int
	ExpiresAt *time.Time
	CreatedAt time.Time
}

type Ban struct {
	UserID    int64
	Username  string
	Name      string
	BannedBy  int64
	Reason    string
	CreatedAt time.Time
}

const guildCols = `id, name, owner_id, created_at`

func scanGuild(row pgx.Row) (Guild, error) {
	var g Guild
	err := row.Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Guild{}, errNotFound
	}
	return g, err
}

// insertGuild grava a guild, o papel @everyone e o dono como membro. Roda dentro da tx do chamador.
func (s *store) insertGuild(ctx context.Context, tx pgx.Tx, id int64, name string, owner int64) (Guild, error) {
	g, err := scanGuild(tx.QueryRow(ctx,
		`INSERT INTO guilds (id, name, owner_id) VALUES ($1, $2, $3) RETURNING `+guildCols, id, name, owner))
	if err != nil {
		return Guild{}, err
	}
	// @everyone: id = id da guild
	if _, err := tx.Exec(ctx,
		`INSERT INTO roles (id, guild_id, name, permissions, position) VALUES ($1, $1, '@everyone', $2, 0)`,
		id, int64(permissions.DefaultEveryone)); err != nil {
		return Guild{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO members (guild_id, user_id) VALUES ($1, $2)`, id, owner); err != nil {
		return Guild{}, err
	}
	return g, nil
}

func (s *store) guild(ctx context.Context, id int64) (Guild, error) {
	return scanGuild(s.db.QueryRow(ctx, `SELECT `+guildCols+` FROM guilds WHERE id = $1`, id))
}

func (s *store) guildsOf(ctx context.Context, user int64) ([]Guild, error) {
	rows, err := s.db.Query(ctx,
		`SELECT g.id, g.name, g.owner_id, g.created_at FROM guilds g JOIN members m ON m.guild_id = g.id
		 WHERE m.user_id = $1 ORDER BY m.joined_at, g.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Guild
	for rows.Next() {
		var g Guild
		if err := rows.Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *store) deleteGuild(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM guilds WHERE id = $1`, id)
	return err
}

// subject carrega o membro e os papéis dele numa consulta só. errNotMember se não for membro.
func (s *store) subject(ctx context.Context, q querier, guild, user int64) (permissions.Subject, error) {
	rows, err := q.Query(ctx,
		`SELECT g.owner_id, r.id, r.position, r.permissions
		 FROM guilds g
		 JOIN members m ON m.guild_id = g.id AND m.user_id = $2
		 JOIN roles r ON r.guild_id = g.id
		   AND (r.id = g.id OR EXISTS (SELECT 1 FROM member_roles mr WHERE mr.guild_id = g.id AND mr.user_id = $2 AND mr.role_id = r.id))
		 WHERE g.id = $1`, guild, user)
	if err != nil {
		return permissions.Subject{}, err
	}
	defer rows.Close()
	sub := permissions.Subject{UserID: user}
	found := false
	for rows.Next() {
		var owner, id int64
		var pos int
		var perms int64
		if err := rows.Scan(&owner, &id, &pos, &perms); err != nil {
			return permissions.Subject{}, err
		}
		found = true
		sub.IsOwner = owner == user
		role := permissions.Role{ID: id, Position: pos, Perms: permissions.Perm(perms)}
		if id == guild {
			sub.Everyone = role
		} else {
			sub.Roles = append(sub.Roles, role)
		}
	}
	if err := rows.Err(); err != nil {
		return permissions.Subject{}, err
	}
	if !found {
		return permissions.Subject{}, errNotMember
	}
	return sub, nil
}

func (s *store) setOwner(ctx context.Context, guild, owner int64) error {
	_, err := s.db.Exec(ctx, `UPDATE guilds SET owner_id = $2 WHERE id = $1`, guild, owner)
	return err
}

// ---- membros ----

const memberSelect = `SELECT m.user_id, u.username, u.name, u.subtitle, u.hue, m.joined_at,
	COALESCE((SELECT array_agg(mr.role_id ORDER BY mr.role_id) FROM member_roles mr WHERE mr.guild_id = m.guild_id AND mr.user_id = m.user_id), '{}')
	FROM members m JOIN users u ON u.id = m.user_id`

func scanMembers(rows pgx.Rows) ([]Member, error) {
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.Name, &m.Subtitle, &m.Hue, &m.JoinedAt, &m.RoleIDs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *store) members(ctx context.Context, guild, after int64, limit int) ([]Member, int, error) {
	rows, err := s.db.Query(ctx, memberSelect+` WHERE m.guild_id = $1 AND m.user_id > $2 ORDER BY m.user_id LIMIT $3`, guild, after, limit)
	if err != nil {
		return nil, 0, err
	}
	items, err := scanMembers(rows)
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = s.db.QueryRow(ctx, `SELECT count(*) FROM members WHERE guild_id = $1`, guild).Scan(&total)
	return items, total, err
}

func (s *store) member(ctx context.Context, guild, user int64) (Member, error) {
	rows, err := s.db.Query(ctx, memberSelect+` WHERE m.guild_id = $1 AND m.user_id = $2`, guild, user)
	if err != nil {
		return Member{}, err
	}
	items, err := scanMembers(rows)
	if err != nil {
		return Member{}, err
	}
	if len(items) == 0 {
		return Member{}, errNotMember
	}
	return items[0], nil
}

func (s *store) removeMember(ctx context.Context, q querier, guild, user int64) error {
	tag, err := q.Exec(ctx, `DELETE FROM members WHERE guild_id = $1 AND user_id = $2`, guild, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotMember
	}
	return nil
}

func (s *store) addMemberRole(ctx context.Context, guild, user, role int64) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO member_roles (guild_id, user_id, role_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, guild, user, role)
	return err
}

func (s *store) removeMemberRole(ctx context.Context, guild, user, role int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM member_roles WHERE guild_id = $1 AND user_id = $2 AND role_id = $3`, guild, user, role)
	return err
}

// ---- papéis ----

const roleCols = `id, guild_id, name, permissions, position`

func scanRole(row pgx.Row) (Role, error) {
	var r Role
	var perms int64
	err := row.Scan(&r.ID, &r.GuildID, &r.Name, &perms, &r.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, errNotFound
	}
	r.Permissions = permissions.Perm(perms)
	r.Everyone = r.ID == r.GuildID
	return r, err
}

func (s *store) roles(ctx context.Context, q querier, guild int64, lock bool) ([]Role, error) {
	sql := `SELECT ` + roleCols + ` FROM roles WHERE guild_id = $1 ORDER BY position, id`
	if lock {
		sql += ` FOR UPDATE`
	}
	rows, err := q.Query(ctx, sql, guild)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) role(ctx context.Context, guild, id int64) (Role, error) {
	return scanRole(s.db.QueryRow(ctx, `SELECT `+roleCols+` FROM roles WHERE guild_id = $1 AND id = $2`, guild, id))
}

// insertRole põe o papel na posição 1 (logo acima de @everyone), empurrando os outros para cima.
func (s *store) insertRole(ctx context.Context, id, guild int64, name string, perms permissions.Perm) (Role, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Role{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE roles SET position = position + 1 WHERE guild_id = $1 AND position >= 1`, guild); err != nil {
		return Role{}, err
	}
	r, err := scanRole(tx.QueryRow(ctx,
		`INSERT INTO roles (id, guild_id, name, permissions, position) VALUES ($1, $2, $3, $4, 1) RETURNING `+roleCols,
		id, guild, name, int64(perms)))
	if err != nil {
		return Role{}, err
	}
	return r, tx.Commit(ctx)
}

func (s *store) updateRole(ctx context.Context, q querier, guild, id int64, name *string, perms *permissions.Perm) (Role, error) {
	var p *int64
	if perms != nil {
		v := int64(*perms)
		p = &v
	}
	return scanRole(q.QueryRow(ctx,
		`UPDATE roles SET name = COALESCE($3, name), permissions = COALESCE($4, permissions)
		 WHERE guild_id = $1 AND id = $2 RETURNING `+roleCols, guild, id, name, p))
}

func (s *store) setRolePositions(ctx context.Context, q querier, order []int64) error {
	for i, id := range order {
		if _, err := q.Exec(ctx, `UPDATE roles SET position = $2 WHERE id = $1`, id, i+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) deleteRole(ctx context.Context, guild, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM roles WHERE guild_id = $1 AND id = $2`, guild, id)
	return err
}

// ---- convites ----

const inviteCols = `code, guild_id, creator_id, max_uses, uses, expires_at, created_at`

func scanInvite(row pgx.Row) (Invite, error) {
	var i Invite
	err := row.Scan(&i.Code, &i.GuildID, &i.CreatorID, &i.MaxUses, &i.Uses, &i.ExpiresAt, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, errNotFound
	}
	return i, err
}

func (s *store) insertInvite(ctx context.Context, i Invite) (Invite, error) {
	return scanInvite(s.db.QueryRow(ctx,
		`INSERT INTO invites (code, guild_id, creator_id, max_uses, expires_at) VALUES ($1, $2, $3, $4, $5) RETURNING `+inviteCols,
		i.Code, i.GuildID, i.CreatorID, i.MaxUses, i.ExpiresAt))
}

func (s *store) invite(ctx context.Context, code string) (Invite, error) {
	return scanInvite(s.db.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE code = $1`, code))
}

func (s *store) invitesOf(ctx context.Context, guild int64) ([]Invite, error) {
	rows, err := s.db.Query(ctx, `SELECT `+inviteCols+` FROM invites WHERE guild_id = $1 ORDER BY created_at, code`, guild)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *store) deleteInvite(ctx context.Context, code string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM invites WHERE code = $1`, code)
	return err
}

// join consome um uso do convite e cria o membro, tudo numa transação que trava o convite
// banned é checado antes de tudo.
func (s *store) join(ctx context.Context, code string, user int64, now time.Time) (Guild, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Guild{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	inv, err := scanInvite(tx.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE code = $1 FOR UPDATE`, code))
	if err != nil {
		return Guild{}, false, err
	}
	var banned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bans WHERE guild_id = $1 AND user_id = $2)`, inv.GuildID, user).Scan(&banned); err != nil {
		return Guild{}, false, err
	}
	if banned {
		return Guild{}, false, errBanned
	}
	g, err := scanGuild(tx.QueryRow(ctx, `SELECT `+guildCols+` FROM guilds WHERE id = $1`, inv.GuildID))
	if err != nil {
		return Guild{}, false, err
	}
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE guild_id = $1 AND user_id = $2)`, inv.GuildID, user).Scan(&member); err != nil {
		return Guild{}, false, err
	}
	if member {
		return g, false, nil
	}
	if (inv.ExpiresAt != nil && !now.Before(*inv.ExpiresAt)) || (inv.MaxUses != nil && inv.Uses >= *inv.MaxUses) {
		return Guild{}, false, errInviteGone
	}
	if _, err := tx.Exec(ctx, `INSERT INTO members (guild_id, user_id) VALUES ($1, $2)`, inv.GuildID, user); err != nil {
		return Guild{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET uses = uses + 1 WHERE code = $1`, code); err != nil {
		return Guild{}, false, err
	}
	return g, true, tx.Commit(ctx)
}

var errBanned = errors.New("guilds: banned")

// ---- bans ----

// ban grava o ban e tira o membro (se for) na mesma transação. Reban atualiza o motivo.
func (s *store) ban(ctx context.Context, guild, user, by int64, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`INSERT INTO bans (guild_id, user_id, banned_by, reason) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (guild_id, user_id) DO UPDATE SET reason = EXCLUDED.reason, banned_by = EXCLUDED.banned_by`,
		guild, user, by, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM members WHERE guild_id = $1 AND user_id = $2`, guild, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *store) unban(ctx context.Context, guild, user int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM bans WHERE guild_id = $1 AND user_id = $2`, guild, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

func (s *store) bans(ctx context.Context, guild int64) ([]Ban, error) {
	rows, err := s.db.Query(ctx,
		`SELECT b.user_id, u.username, u.name, b.banned_by, b.reason, b.created_at
		 FROM bans b JOIN users u ON u.id = b.user_id WHERE b.guild_id = $1 ORDER BY b.created_at, b.user_id`, guild)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ban
	for rows.Next() {
		var b Ban
		if err := rows.Scan(&b.UserID, &b.Username, &b.Name, &b.BannedBy, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func isFKViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}
