package channels

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/permissions"
)

var errNotFound = errors.New("channels: not found")

type Kind string

const (
	KindText     Kind = "text"
	KindVoice    Kind = "voice"
	KindCategory Kind = "category"
)

type Channel struct {
	ID        int64
	GuildID   int64
	ParentID  *int64
	Kind      Kind
	Name      string
	Position  int
	CreatedAt time.Time
}

type store struct{ db *pgxpool.Pool }

const channelCols = `id, guild_id, parent_id, kind, name, position, created_at`

func scanChannel(row pgx.Row) (Channel, error) {
	var c Channel
	err := row.Scan(&c.ID, &c.GuildID, &c.ParentID, &c.Kind, &c.Name, &c.Position, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, errNotFound
	}
	return c, err
}

func (s *store) channel(ctx context.Context, id int64) (Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelCols+` FROM channels WHERE id = $1`, id))
}

func (s *store) channelsOf(ctx context.Context, guild int64) ([]Channel, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+channelCols+` FROM channels WHERE guild_id = $1 ORDER BY position, id`, guild)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// insert põe o canal no fim do grupo de irmãos (mesmo parent).
func (s *store) insert(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, id, guild int64, parent *int64, kind Kind, name string) (Channel, error) {
	return scanChannel(q.QueryRow(ctx,
		`INSERT INTO channels (id, guild_id, parent_id, kind, name, position)
		 VALUES ($1, $2, $3, $4, $5,
		   COALESCE((SELECT max(position) + 1 FROM channels WHERE guild_id = $2 AND parent_id IS NOT DISTINCT FROM $3), 0))
		 RETURNING `+channelCols, id, guild, parent, string(kind), name))
}

func (s *store) delete(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM channels WHERE id = $1`, id)
	return err
}

// update aplica nome e/ou pai e, se pedido, reordena no grupo de destino, numa transação.
// parentChange: nil = não muda; senão *parentChange (que pode apontar para nil = sem categoria).
func (s *store) update(ctx context.Context, id int64, name *string, parentChange **int64, position *int) (Channel, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cur, err := scanChannel(tx.QueryRow(ctx, `SELECT `+channelCols+` FROM channels WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return Channel{}, err
	}
	if name != nil {
		if _, err := tx.Exec(ctx, `UPDATE channels SET name = $2 WHERE id = $1`, id, *name); err != nil {
			return Channel{}, err
		}
	}
	newParent := cur.ParentID
	if parentChange != nil {
		newParent = *parentChange
	}
	moved := parentChange != nil && !sameID(cur.ParentID, newParent)
	if moved || position != nil {
		// irmãos do destino, sem o próprio canal
		rows, err := tx.Query(ctx,
			`SELECT id FROM channels WHERE guild_id = $1 AND parent_id IS NOT DISTINCT FROM $2 AND id <> $3
			 ORDER BY position, id FOR UPDATE`, cur.GuildID, newParent, id)
		if err != nil {
			return Channel{}, err
		}
		var order []int64
		for rows.Next() {
			var sib int64
			if err := rows.Scan(&sib); err != nil {
				rows.Close()
				return Channel{}, err
			}
			order = append(order, sib)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Channel{}, err
		}
		idx := len(order) // só trocou de categoria: vai para o fim
		if position != nil {
			idx = min(max(*position, 0), len(order))
		} else if !moved {
			idx = cur.Position
		}
		order = append(order[:idx], append([]int64{id}, order[idx:]...)...)
		if _, err := tx.Exec(ctx, `UPDATE channels SET parent_id = $2 WHERE id = $1`, id, newParent); err != nil {
			return Channel{}, err
		}
		for i, cid := range order {
			if _, err := tx.Exec(ctx, `UPDATE channels SET position = $2 WHERE id = $1`, cid, i); err != nil {
				return Channel{}, err
			}
		}
	}
	out, err := scanChannel(tx.QueryRow(ctx, `SELECT `+channelCols+` FROM channels WHERE id = $1`, id))
	if err != nil {
		return Channel{}, err
	}
	return out, tx.Commit(ctx)
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ---- overrides ----

func scanOverrides(rows pgx.Rows) (map[int64][]permissions.Override, error) {
	defer rows.Close()
	out := map[int64][]permissions.Override{}
	for rows.Next() {
		var ch, target, allow, deny int64
		var kind string
		if err := rows.Scan(&ch, &kind, &target, &allow, &deny); err != nil {
			return nil, err
		}
		out[ch] = append(out[ch], permissions.Override{
			Kind: permissions.TargetKind(kind), TargetID: target, Allow: permissions.Perm(allow), Deny: permissions.Perm(deny)})
	}
	return out, rows.Err()
}

const overrideCols = `channel_id, target_kind, target_id, allow, deny`

func (s *store) overridesOfGuild(ctx context.Context, guild int64) (map[int64][]permissions.Override, error) {
	rows, err := s.db.Query(ctx,
		`SELECT o.channel_id, o.target_kind, o.target_id, o.allow, o.deny
		 FROM channel_overrides o JOIN channels c ON c.id = o.channel_id WHERE c.guild_id = $1`, guild)
	if err != nil {
		return nil, err
	}
	return scanOverrides(rows)
}

func (s *store) overrides(ctx context.Context, channel int64) ([]permissions.Override, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+overrideCols+` FROM channel_overrides WHERE channel_id = $1 ORDER BY target_kind, target_id`, channel)
	if err != nil {
		return nil, err
	}
	m, err := scanOverrides(rows)
	return m[channel], err
}

func (s *store) setOverride(ctx context.Context, channel int64, o permissions.Override) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO channel_overrides (channel_id, target_kind, target_id, allow, deny) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (channel_id, target_kind, target_id) DO UPDATE SET allow = EXCLUDED.allow, deny = EXCLUDED.deny`,
		channel, string(o.Kind), o.TargetID, int64(o.Allow), int64(o.Deny))
	return err
}

func (s *store) deleteOverride(ctx context.Context, channel int64, kind permissions.TargetKind, target int64) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM channel_overrides WHERE channel_id = $1 AND target_kind = $2 AND target_id = $3`, channel, string(kind), target)
	return err
}
