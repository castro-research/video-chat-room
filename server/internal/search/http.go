package search

import (
	"net/http"
	"time"

	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

type AuthorView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type ResultView struct {
	ID        string     `json:"id"`
	ChannelID string     `json:"channelId"`
	Author    AuthorView `json:"author"`
	CreatedAt time.Time  `json:"createdAt"`
	Text      string     `json:"text"`
	Highlight string     `json:"highlight,omitempty"`
}

func (s *Service) Mount(mux *http.ServeMux, auth func(httpx.Handler) httpx.Handler) {
	mux.HandleFunc("GET /v1/guilds/{guildId}/search", httpx.Wrap(auth(s.handleSearch)))
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	guild, err := httpx.PathID(r, "guildId")
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", DefaultLimit, 1, MaxLimit)
	if err != nil {
		return err
	}
	vals := r.URL.Query()
	if err := httpx.NoNUL(vals.Get("q")); err != nil {
		return err
	}
	q := Query{Text: vals.Get("q"), Limit: limit, Highlight: vals.Get("highlight") == "true"}
	for name, dst := range map[string]*int64{"channelId": &q.ChannelID, "authorId": &q.AuthorID, "cursor": &q.Cursor} {
		if raw := vals.Get(name); raw != "" {
			id, err := ids.Parse(raw)
			if err != nil {
				return httpx.Validation(name + " is invalid")
			}
			*dst = id
		}
	}
	p, err := s.Search(r.Context(), user, guild, q)
	if err != nil {
		return err
	}
	items := make([]ResultView, len(p.Items))
	for i, x := range p.Items {
		items[i] = ResultView{ID: ids.Format(x.ID), ChannelID: ids.Format(x.ChannelID), CreatedAt: x.CreatedAt, Text: x.Text, Highlight: x.Highlight,
			Author: AuthorView{ID: ids.Format(x.Author.ID), Username: x.Author.Username, Name: x.Author.Name}}
	}
	out := map[string]any{"items": items}
	if p.NextCursor != 0 {
		out["nextCursor"] = ids.Format(p.NextCursor)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
