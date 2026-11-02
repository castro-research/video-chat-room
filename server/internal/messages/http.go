package messages

import (
	"net/http"
	"time"

	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

type UserView struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Subtitle string   `json:"subtitle,omitempty"`
	Hue      *float64 `json:"hue,omitempty"`
}

type ReactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Me    bool   `json:"me"`
}

type MessageView struct {
	ID          string         `json:"id"`
	ChannelID   string         `json:"channelId"`
	Author      UserView       `json:"author"`
	CreatedAt   time.Time      `json:"createdAt"`
	EditedAt    *time.Time     `json:"editedAt,omitempty"`
	Text        string         `json:"text"`
	ReplyToID   string         `json:"replyToId,omitempty"`
	Attachments []struct{}     `json:"attachments"` // Plano 5
	Reactions   []ReactionView `json:"reactions"`
	Nonce       string         `json:"nonce,omitempty"`
}

func messageView(m Message) MessageView {
	v := MessageView{
		ID: ids.Format(m.ID), ChannelID: ids.Format(m.ChannelID),
		Author: UserView{ID: ids.Format(m.Author.ID), Username: m.Author.Username, Name: m.Author.Name,
			Status: "offline", Subtitle: m.Author.Subtitle, Hue: m.Author.Hue},
		CreatedAt: m.CreatedAt, EditedAt: m.EditedAt, Text: m.Content,
		Attachments: []struct{}{}, Reactions: make([]ReactionView, len(m.Reactions)), Nonce: m.Nonce,
	}
	if m.ReplyToID != nil {
		v.ReplyToID = ids.Format(*m.ReplyToID)
	}
	for i, r := range m.Reactions {
		v.Reactions[i] = ReactionView{Emoji: r.Emoji, Count: r.Count, Me: r.Me}
	}
	return v
}

type ReadStateView struct {
	ChannelID         string `json:"channelId"`
	LastReadMessageID string `json:"lastReadMessageId,omitempty"`
	Unread            int    `json:"unread"`
}

func readStateView(r ReadState) ReadStateView {
	v := ReadStateView{ChannelID: ids.Format(r.ChannelID), Unread: r.Unread}
	if r.LastReadMessageID != 0 {
		v.LastReadMessageID = ids.Format(r.LastReadMessageID)
	}
	return v
}

func (s *Service) Mount(mux *http.ServeMux, auth func(httpx.Handler) httpx.Handler) {
	h := func(pattern string, fn httpx.Handler) { mux.HandleFunc(pattern, httpx.Wrap(auth(fn))) }
	h("POST /v1/channels/{channelId}/messages", s.handleSend)
	h("GET /v1/channels/{channelId}/messages", s.handleList)
	h("PATCH /v1/channels/{channelId}/messages/{messageId}", s.handleEdit)
	h("DELETE /v1/channels/{channelId}/messages/{messageId}", s.handleDelete)
	h("PUT /v1/channels/{channelId}/messages/{messageId}/reactions/{emoji}/@me", s.handleAddReaction)
	h("DELETE /v1/channels/{channelId}/messages/{messageId}/reactions/{emoji}/{userId}", s.handleRemoveReaction)
	h("POST /v1/channels/{channelId}/read", s.handleAck)
	h("POST /v1/channels/{channelId}/ack", s.handleAck) // apelido de /read
	h("GET /v1/channels/{channelId}/read", s.handleChannelRead)
	h("GET /v1/guilds/{guildId}/read", s.handleGuildRead)
}

// ids lê o usuário e os ids de caminho pedidos (channelId e, se withMessage, messageId).
func route(r *http.Request, withMessage bool) (user, channel, message int64, err error) {
	if user, err = httpx.Caller(r); err != nil {
		return
	}
	if channel, err = httpx.PathID(r, "channelId"); err != nil {
		return
	}
	if withMessage {
		message, err = httpx.PathID(r, "messageId")
	}
	return
}

func (s *Service) handleSend(w http.ResponseWriter, r *http.Request) error {
	user, channel, _, err := route(r, false)
	if err != nil {
		return err
	}
	var req struct {
		Text      string `json:"text"`
		ReplyToID string `json:"replyToId"`
		Nonce     string `json:"nonce"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	in := SendInput{Text: req.Text, Nonce: req.Nonce}
	if req.ReplyToID != "" {
		id, err := ids.Parse(req.ReplyToID)
		if err != nil {
			return httpx.Validation("replyToId is invalid")
		}
		in.ReplyToID = &id
	}
	m, err := s.Send(r.Context(), user, channel, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, messageView(m))
	return nil
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) error {
	user, channel, _, err := route(r, false)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", DefaultPageLimit, 1, MaxPageLimit)
	if err != nil {
		return err
	}
	in := ListInput{Limit: limit}
	q := r.URL.Query()
	n := 0
	for name, dir := range map[string]Direction{"before": Before, "after": After, "around": Around} {
		raw := q.Get(name)
		if raw == "" {
			continue
		}
		id, err := ids.Parse(raw)
		if err != nil {
			return httpx.Validation(name + " is invalid")
		}
		in.Dir, in.Cursor = dir, id
		n++
	}
	if n > 1 {
		return httpx.Validation("use only one of before, after and around")
	}
	p, err := s.List(r.Context(), user, channel, in)
	if err != nil {
		return err
	}
	items := make([]MessageView, len(p.Items))
	for i, m := range p.Items {
		items[i] = messageView(m)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "hasMore": p.HasMore, "hasNewer": p.HasNewer})
	return nil
}

func (s *Service) handleEdit(w http.ResponseWriter, r *http.Request) error {
	user, channel, id, err := route(r, true)
	if err != nil {
		return err
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	m, err := s.Edit(r.Context(), user, channel, id, req.Text)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, messageView(m))
	return nil
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) error {
	user, channel, id, err := route(r, true)
	if err != nil {
		return err
	}
	if err := s.Delete(r.Context(), user, channel, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleAddReaction(w http.ResponseWriter, r *http.Request) error {
	user, channel, id, err := route(r, true)
	if err != nil {
		return err
	}
	if err := s.AddReaction(r.Context(), user, channel, id, r.PathValue("emoji")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleRemoveReaction(w http.ResponseWriter, r *http.Request) error {
	user, channel, id, err := route(r, true)
	if err != nil {
		return err
	}
	var target int64 // 0 = @me
	if raw := r.PathValue("userId"); raw != "@me" {
		if target, err = httpx.PathID(r, "userId"); err != nil {
			return err
		}
	}
	if err := s.RemoveReaction(r.Context(), user, channel, id, r.PathValue("emoji"), target); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleAck(w http.ResponseWriter, r *http.Request) error {
	user, channel, _, err := route(r, false)
	if err != nil {
		return err
	}
	var req struct {
		MessageID string `json:"messageId"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	id, err := ids.Parse(req.MessageID)
	if err != nil {
		return httpx.Validation("messageId is invalid")
	}
	if err := s.Ack(r.Context(), user, channel, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleChannelRead(w http.ResponseWriter, r *http.Request) error {
	user, channel, _, err := route(r, false)
	if err != nil {
		return err
	}
	rs, err := s.ChannelReadState(r.Context(), user, channel)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, readStateView(rs))
	return nil
}

func (s *Service) handleGuildRead(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	guild, err := httpx.PathID(r, "guildId")
	if err != nil {
		return err
	}
	rs, err := s.GuildReadStates(r.Context(), user, guild)
	if err != nil {
		return err
	}
	items := make([]ReadStateView, len(rs))
	for i, x := range rs {
		items[i] = readStateView(x)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}
