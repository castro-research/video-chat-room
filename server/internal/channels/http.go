package channels

import (
	"encoding/json"
	"net/http"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

type ChannelView struct {
	ID       string `json:"id"`
	GuildID  string `json:"guildId"`
	Name     string `json:"name"`
	Kind     Kind   `json:"kind"`
	ParentID string `json:"parentId,omitempty"`
	Position int    `json:"position"`
}

func channelView(c Channel) ChannelView {
	v := ChannelView{ID: ids.Format(c.ID), GuildID: ids.Format(c.GuildID), Name: c.Name, Kind: c.Kind, Position: c.Position}
	if c.ParentID != nil {
		v.ParentID = ids.Format(*c.ParentID)
	}
	return v
}

type OverrideView struct {
	Kind     permissions.TargetKind `json:"kind"`
	TargetID string                 `json:"targetId"`
	Allow    uint64                 `json:"allow"`
	Deny     uint64                 `json:"deny"`
}

func overrideView(o permissions.Override) OverrideView {
	return OverrideView{Kind: o.Kind, TargetID: ids.Format(o.TargetID), Allow: uint64(o.Allow), Deny: uint64(o.Deny)}
}

func (s *Service) Mount(mux *http.ServeMux, auth func(httpx.Handler) httpx.Handler) {
	h := func(pattern string, fn httpx.Handler) { mux.HandleFunc(pattern, httpx.Wrap(auth(fn))) }
	h("GET /v1/guilds/{guildId}/channels", s.handleList)
	h("POST /v1/guilds/{guildId}/channels", s.handleCreate)
	h("GET /v1/channels/{channelId}", s.handleGet)
	h("PATCH /v1/channels/{channelId}", s.handleUpdate)
	h("DELETE /v1/channels/{channelId}", s.handleDelete)
	h("GET /v1/channels/{channelId}/overrides", s.handleOverrides)
	h("PUT /v1/channels/{channelId}/overrides/{kind}/{targetId}", s.handleSetOverride)
	h("DELETE /v1/channels/{channelId}/overrides/{kind}/{targetId}", s.handleDeleteOverride)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	guild, err := httpx.PathID(r, "guildId")
	if err != nil {
		return err
	}
	cs, err := s.List(r.Context(), user, guild)
	if err != nil {
		return err
	}
	items := make([]ChannelView, len(cs))
	for i, c := range cs {
		items[i] = channelView(c)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	guild, err := httpx.PathID(r, "guildId")
	if err != nil {
		return err
	}
	var req struct {
		Name     string `json:"name"`
		Kind     Kind   `json:"kind"`
		ParentID string `json:"parentId"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	in := CreateInput{Name: req.Name, Kind: req.Kind}
	if req.ParentID != "" {
		p, err := ids.Parse(req.ParentID)
		if err != nil {
			return httpx.Validation("parentId is invalid")
		}
		in.ParentID = &p
	}
	c, err := s.Create(r.Context(), user, guild, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, channelView(c))
	return nil
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	c, err := s.Get(r.Context(), user, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, channelView(c))
	return nil
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	var req struct {
		Name     *string         `json:"name"`
		ParentID json.RawMessage `json:"parentId"` // ausente = não mexe; null = tira da categoria
		Position *int            `json:"position"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	in := UpdateInput{Name: req.Name, Position: req.Position}
	if req.ParentID != nil {
		var raw *string
		if err := json.Unmarshal(req.ParentID, &raw); err != nil {
			return httpx.Validation("parentId is invalid")
		}
		if raw == nil {
			in.ClearParent = true
		} else {
			p, err := ids.Parse(*raw)
			if err != nil {
				return httpx.Validation("parentId is invalid")
			}
			in.ParentID = &p
		}
	}
	c, err := s.Update(r.Context(), user, id, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, channelView(c))
	return nil
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	if err := s.Delete(r.Context(), user, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleOverrides(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	os, err := s.Overrides(r.Context(), user, id)
	if err != nil {
		return err
	}
	items := make([]OverrideView, len(os))
	for i, o := range os {
		items[i] = overrideView(o)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func overrideTarget(r *http.Request) (permissions.TargetKind, int64, error) {
	kind := permissions.TargetKind(r.PathValue("kind"))
	if kind != permissions.TargetRole && kind != permissions.TargetMember {
		return "", 0, httpx.ErrNotFound
	}
	target, err := httpx.PathID(r, "targetId")
	return kind, target, err
}

func (s *Service) handleSetOverride(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	kind, target, err := overrideTarget(r)
	if err != nil {
		return err
	}
	var req struct {
		Allow uint64 `json:"allow"`
		Deny  uint64 `json:"deny"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	o, err := s.SetOverride(r.Context(), user, id, permissions.Override{
		Kind: kind, TargetID: target, Allow: permissions.Perm(req.Allow), Deny: permissions.Perm(req.Deny)})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, overrideView(o))
	return nil
}

func (s *Service) handleDeleteOverride(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	id, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	kind, target, err := overrideTarget(r)
	if err != nil {
		return err
	}
	if err := s.DeleteOverride(r.Context(), user, id, kind, target); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
