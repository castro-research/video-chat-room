package messages

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("messages: not found")

type Author struct {
	ID       int64
	Username string
	Name     string
	Subtitle string
	Hue      *float64
}

// ReactionCount é um emoji agregado numa mensagem; Me diz se o leitor reagiu com ele.
type ReactionCount struct {
	Emoji string
	Count int
	Me    bool
}

type Message struct {
	ID          int64
	ChannelID   int64
	Author      Author
	Content     string
	ReplyToID   *int64
	CreatedAt   time.Time
	EditedAt    *time.Time
	Reactions   []ReactionCount
	Attachments []Attachment
	Nonce       string // só no eco ao remetente; nunca persistido
}

type ReadState struct {
	ChannelID         int64
	LastReadMessageID int64 // 0 = nunca leu
	Unread            int
}

type store struct{ db *pgxpool.Pool }

// querier é o que pool e transação têm em comum.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const messageSelect = `SELECT m.id, m.channel_id, u.id, u.username, u.name, u.subtitle, u.hue,
	m.content, m.reply_to_id, m.created_at, m.edited_at
	FROM messages m JOIN users u ON u.id = m.author_id `

func scanMessage(row pgx.Row) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.ChannelID, &m.Author.ID, &m.Author.Username, &m.Author.Name, &m.Author.Subtitle, &m.Author.Hue,
		&m.Content, &m.ReplyToID, &m.CreatedAt, &m.EditedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, errNotFound
	}
	return m, err
}

// message devolve a mensagem sem reações, só se ela estiver no canal.
func (s *store) message(ctx context.Context, q querier, channel, id int64) (Message, error) {
	return scanMessage(q.QueryRow(ctx, messageSelect+`WHERE m.id = $1 AND m.channel_id = $2`, id, channel))
}

// lockChannel serializa as escritas do canal até o fim da transação, para que a ordem de commit
// seja a ordem dos ids (sem isso, um leitor com cursor `after` poderia pular uma mensagem que
// commitou atrasada com id menor).
func (s *store) lockChannel(ctx context.Context, tx pgx.Tx, channel int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, channel)
	return err
}

func (s *store) insert(ctx context.Context, tx pgx.Tx, id, channel, author int64, content string, replyTo *int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO messages (id, channel_id, author_id, content, reply_to_id) VALUES ($1, $2, $3, $4, $5)`,
		id, channel, author, content, replyTo)
	return err
}

func (s *store) update(ctx context.Context, tx pgx.Tx, channel, id int64, content string) error {
	_, err := tx.Exec(ctx, `UPDATE messages SET content = $3, edited_at = now() WHERE id = $1 AND channel_id = $2`, id, channel, content)
	return err
}

func (s *store) delete(ctx context.Context, tx pgx.Tx, channel, id int64) error {
	_, err := tx.Exec(ctx, `DELETE FROM messages WHERE id = $1 AND channel_id = $2`, id, channel)
	return err
}

// lockMessage trava a linha da mensagem (FOR UPDATE) e confirma que ela existe no canal.
func (s *store) lockMessage(ctx context.Context, tx pgx.Tx, channel, id int64) error {
	var x int64
	err := tx.QueryRow(ctx, `SELECT id FROM messages WHERE id = $1 AND channel_id = $2 FOR UPDATE`, id, channel).Scan(&x)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	return err
}

type pageMode int

const (
	pageLatest pageMode = iota
	pageBefore
	pageAfter
	pageAround
)

// page devolve até limit mensagens em ordem crescente de id.
//   - latest: as mais novas; before: as mais novas com id < cursor; after: as mais antigas com id > cursor;
//   - around: ~metade com id < cursor e o resto com id >= cursor (a própria mensagem entra se existir).
func (s *store) page(ctx context.Context, channel int64, mode pageMode, cursor int64, limit int) ([]Message, error) {
	older := func(where string, args []any, n int) ([]Message, error) {
		ms, err := s.fetch(ctx, where+` ORDER BY m.id DESC LIMIT `+itoa(n), args...)
		for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
			ms[i], ms[j] = ms[j], ms[i]
		}
		return ms, err
	}
	switch mode {
	case pageBefore:
		return older(`WHERE m.channel_id = $1 AND m.id < $2`, []any{channel, cursor}, limit)
	case pageAfter:
		return s.fetch(ctx, `WHERE m.channel_id = $1 AND m.id > $2 ORDER BY m.id ASC LIMIT `+itoa(limit), channel, cursor)
	case pageAround:
		half := limit / 2
		before, err := older(`WHERE m.channel_id = $1 AND m.id < $2`, []any{channel, cursor}, half)
		if err != nil {
			return nil, err
		}
		from, err := s.fetch(ctx, `WHERE m.channel_id = $1 AND m.id >= $2 ORDER BY m.id ASC LIMIT `+itoa(limit-half), channel, cursor)
		if err != nil {
			return nil, err
		}
		return append(before, from...), nil
	default:
		return older(`WHERE m.channel_id = $1`, []any{channel}, limit)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *store) fetch(ctx context.Context, tail string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(ctx, messageSelect+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *store) hasOlder(ctx context.Context, q querier, channel, id int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE channel_id = $1 AND id < $2)`, channel, id).Scan(&ok)
	return ok, err
}

func (s *store) hasNewer(ctx context.Context, q querier, channel, id int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE channel_id = $1 AND id > $2)`, channel, id).Scan(&ok)
	return ok, err
}

// attachReactions preenche Reactions de ms com as contagens e o "eu" de viewer.
func (s *store) attachReactions(ctx context.Context, q querier, ms []Message, viewer int64) error {
	if len(ms) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(ms))
	ids := make([]int64, len(ms))
	for i, m := range ms {
		idx[m.ID] = i
		ids[i] = m.ID
	}
	rows, err := q.Query(ctx,
		`SELECT message_id, emoji, count(*), bool_or(user_id = $2)
		 FROM reactions WHERE message_id = ANY($1)
		 GROUP BY message_id, emoji ORDER BY message_id, min(created_at), emoji`, ids, viewer)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mid int64
		var r ReactionCount
		if err := rows.Scan(&mid, &r.Emoji, &r.Count, &r.Me); err != nil {
			return err
		}
		ms[idx[mid]].Reactions = append(ms[idx[mid]].Reactions, r)
	}
	return rows.Err()
}

// ---- reações ----

func (s *store) distinctEmojis(ctx context.Context, tx pgx.Tx, message int64, emoji string) (n int, exists bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT count(DISTINCT emoji), COALESCE(bool_or(emoji = $2), false) FROM reactions WHERE message_id = $1`, message, emoji).Scan(&n, &exists)
	return
}

// addReaction diz se a reação era nova; os clientes tratam cada evento como delta, então só mudança gera evento.
func (s *store) addReaction(ctx context.Context, tx pgx.Tx, message, user int64, emoji string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO reactions (message_id, user_id, emoji) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, message, user, emoji)
	return tag.RowsAffected() > 0, err
}

// removeReaction diz se havia reação para remover.
func (s *store) removeReaction(ctx context.Context, tx pgx.Tx, message, user int64, emoji string) (bool, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3`, message, user, emoji)
	return tag.RowsAffected() > 0, err
}

// ---- read state ----

func (s *store) messageExists(ctx context.Context, channel, id int64) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE id = $1 AND channel_id = $2)`, id, channel).Scan(&ok)
	return ok, err
}

// ack só avança: ler uma mensagem mais antiga não desfaz a leitura.
func (s *store) ack(ctx context.Context, tx pgx.Tx, user, channel, message int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO read_states (user_id, channel_id, last_read_message_id) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, channel_id) DO UPDATE
		 SET last_read_message_id = GREATEST(read_states.last_read_message_id, EXCLUDED.last_read_message_id), updated_at = now()`,
		user, channel, message)
	return err
}

// unreadCap limita a contagem de não lidas (basta para o badge "999+").
const unreadCap = 1000

// readStates devolve o estado de leitura dos canais; não lidas ignoram as mensagens do próprio usuário.
func (s *store) readStates(ctx context.Context, user int64, channels []int64) ([]ReadState, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.id, COALESCE(rs.last_read_message_id, 0),
		   (SELECT count(*) FROM (
		      SELECT 1 FROM messages m
		      WHERE m.channel_id = c.id AND m.id > COALESCE(rs.last_read_message_id, 0) AND m.author_id <> $1
		      LIMIT `+itoa(unreadCap)+`) x)
		 FROM unnest($2::bigint[]) AS c(id)
		 LEFT JOIN read_states rs ON rs.channel_id = c.id AND rs.user_id = $1
		 ORDER BY c.id`, user, channels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadState
	for rows.Next() {
		var r ReadState
		if err := rows.Scan(&r.ChannelID, &r.LastReadMessageID, &r.Unread); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
