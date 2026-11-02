package voice

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// row é um estado de voz já com os dados públicos do usuário.
type row struct {
	UserID    int64
	GuildID   int64
	ChannelID int64
	Muted     bool
	CamOn     bool
	Sharing   bool
	Username  string
	Name      string
	Subtitle  string
	Hue       *float64
}

const rowCols = `v.user_id, v.guild_id, v.channel_id, v.muted, v.cam_on, v.sharing, u.username, u.name, u.subtitle, u.hue`

func scanRow(r pgx.Row) (row, error) {
	var x row
	err := r.Scan(&x.UserID, &x.GuildID, &x.ChannelID, &x.Muted, &x.CamOn, &x.Sharing, &x.Username, &x.Name, &x.Subtitle, &x.Hue)
	return x, err
}

func collect(rows pgx.Rows) ([]row, error) {
	defer rows.Close()
	var out []row
	for rows.Next() {
		x, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func channelRows(ctx context.Context, db q, channelID int64) ([]row, error) {
	rows, err := db.Query(ctx, `SELECT `+rowCols+` FROM voice_states v JOIN users u ON u.id = v.user_id
		WHERE v.channel_id = $1 ORDER BY v.joined_at, v.user_id`, channelID)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// lockUser serializa as operações de voz de um mesmo usuário (join/move/leave concorrentes) até o fim da transação.
func lockUser(ctx context.Context, tx pgx.Tx, user int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, user)
	return err
}

func currentRow(ctx context.Context, db q, user int64) (*row, error) {
	x, err := scanRow(db.QueryRow(ctx, `SELECT `+rowCols+` FROM voice_states v JOIN users u ON u.id = v.user_id WHERE v.user_id = $1`, user))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// upsert grava o estado do usuário no canal (move se já estava em outro). Devolve a linha completa.
func upsert(ctx context.Context, tx pgx.Tx, user, guild, channel int64, muted, camOn, sharing bool) (row, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO voice_states (user_id, guild_id, channel_id, muted, cam_on, sharing)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET guild_id = $2, channel_id = $3, muted = $4, cam_on = $5, sharing = $6,
			joined_at = CASE WHEN voice_states.channel_id = $3 THEN voice_states.joined_at ELSE now() END, updated_at = now()`,
		user, guild, channel, muted, camOn, sharing)
	if err != nil {
		return row{}, err
	}
	x, err := currentRow(ctx, tx, user)
	if err != nil {
		return row{}, err
	}
	return *x, nil
}

// deleteWhere apaga as linhas que casam com cond e devolve o que foi apagado (com dados do usuário).
func deleteWhere(ctx context.Context, tx pgx.Tx, cond string, args ...any) ([]row, error) {
	rows, err := tx.Query(ctx, `WITH d AS (DELETE FROM voice_states v WHERE `+cond+` RETURNING v.*)
		SELECT v.user_id, v.guild_id, v.channel_id, v.muted, v.cam_on, v.sharing, u.username, u.name, u.subtitle, u.hue
		FROM d v JOIN users u ON u.id = v.user_id ORDER BY v.user_id`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// guildRows são os estados de voz das guilds de que o usuário é membro.
func guildRows(ctx context.Context, db q, user int64) ([]row, error) {
	rows, err := db.Query(ctx, `SELECT `+rowCols+` FROM voice_states v JOIN users u ON u.id = v.user_id
		WHERE v.guild_id IN (SELECT guild_id FROM members WHERE user_id = $1)
		ORDER BY v.channel_id, v.joined_at, v.user_id`, user)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}
