// Package search consulta as mensagens por texto (RF15, RNF3) com o tsvector gerado em `messages.search`.
//
// Só aparece o que o usuário pode ver: o conjunto de canais vem de Perms (definida aqui, implementada no server
// por cima de channels) e entra na consulta como `channel_id = ANY(...)`. Este pacote não importa guilds nem channels.
//
// Ordem: mais recentes primeiro (id decrescente), com cursor por id. É a ordem natural de um chat e permite
// parar no LIMIT sem pontuar todos os resultados; ranquear por ts_rank exigiria ler e pontuar todo o conjunto que
// casa, o que estoura RNF3 em termos comuns.
package search

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/httpx"
)

const (
	DefaultLimit = 25
	MaxLimit     = 100
	MaxQueryLen  = 200
	// Delimitadores do destaque. Não são HTML (o texto da mensagem não é escapado): o cliente troca por estilo.
	HighlightStart = "⟦"
	HighlightStop  = "⟧"
)

// Perms: VisibleTextChannels devolve os canais de texto que o usuário vê na guild; 404 se não é membro.
type Perms interface {
	VisibleTextChannels(ctx context.Context, userID, guildID int64) ([]int64, error)
}

type Service struct {
	pool  *pgxpool.Pool
	perms Perms
}

func NewService(pool *pgxpool.Pool, perms Perms) *Service { return &Service{pool: pool, perms: perms} }

type Query struct {
	Text      string
	ChannelID int64 // 0 = todos os visíveis
	AuthorID  int64 // 0 = qualquer autor
	Limit     int
	Cursor    int64 // devolve só ids menores que Cursor
	Highlight bool
}

type Author struct {
	ID       int64
	Username string
	Name     string
}

type Result struct {
	ID        int64
	ChannelID int64
	Author    Author
	CreatedAt time.Time
	Text      string
	Highlight string // vazio sem Query.Highlight
}

type Page struct {
	Items      []Result
	NextCursor int64 // 0 = fim
}

func (s *Service) Search(ctx context.Context, user, guild int64, q Query) (Page, error) {
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" || utf8.RuneCountInString(q.Text) > MaxQueryLen {
		return Page{}, httpx.Validation("q must have 1-200 characters")
	}
	if q.Limit < 1 || q.Limit > MaxLimit {
		q.Limit = DefaultLimit
	}
	visible, err := s.perms.VisibleTextChannels(ctx, user, guild)
	if err != nil {
		return Page{}, err
	}
	chans := visible
	if q.ChannelID != 0 {
		chans = nil
		for _, c := range visible {
			if c == q.ChannelID {
				chans = []int64{c}
			}
		}
		if chans == nil { // canal de outra guild, de voz ou que o usuário não vê: não revela que existe
			return Page{}, httpx.ErrNotFound
		}
	}
	if len(chans) == 0 {
		return Page{}, nil
	}

	sql, args := buildSQL(q, chans)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var p Page
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.ChannelID, &r.CreatedAt, &r.Text, &r.Author.ID, &r.Author.Username, &r.Author.Name, &r.Highlight); err != nil {
			return Page{}, err
		}
		p.Items = append(p.Items, r)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(p.Items) > q.Limit {
		p.Items = p.Items[:q.Limit]
		p.NextCursor = p.Items[q.Limit-1].ID
	}
	return p, nil
}

func buildSQL(q Query, chans []int64) (string, []any) {
	args := []any{chans, q.Text}
	where := `m.channel_id = ANY($1) AND m.search @@ websearch_to_tsquery('portuguese'::regconfig, $2)`
	if q.AuthorID != 0 {
		args = append(args, q.AuthorID)
		where += fmt.Sprintf(" AND m.author_id = $%d", len(args))
	}
	if q.Cursor != 0 {
		args = append(args, q.Cursor)
		where += fmt.Sprintf(" AND m.id < $%d", len(args))
	}
	args = append(args, q.Limit+1)
	limitArg := len(args)
	hl := `''`
	if q.Highlight {
		// ts_headline é caro: roda só nas linhas da página (a CTE já aplicou o LIMIT).
		hl = fmt.Sprintf(`ts_headline('portuguese'::regconfig, h.content, websearch_to_tsquery('portuguese'::regconfig, $2),
			'StartSel=%s,StopSel=%s,MaxFragments=2,MaxWords=25,MinWords=8,FragmentDelimiter= … ')`, HighlightStart, HighlightStop)
	}
	sql := fmt.Sprintf(`
		WITH hits AS (
			SELECT m.id, m.channel_id, m.author_id, m.content, m.created_at
			FROM messages m
			WHERE %s
			ORDER BY m.id DESC
			LIMIT $%d)
		SELECT h.id, h.channel_id, h.created_at, h.content, u.id, u.username, u.name, %s
		FROM hits h JOIN users u ON u.id = h.author_id
		ORDER BY h.id DESC`, where, limitArg, hl)
	return sql, args
}
