package guilds

import (
	"net/http"
	"time"

	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

type GuildView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"ownerId"`
	CreatedAt time.Time `json:"createdAt"`
}

func guildView(g Guild) GuildView {
	return GuildView{ID: ids.Format(g.ID), Name: g.Name, OwnerID: ids.Format(g.OwnerID), CreatedAt: g.CreatedAt}
}

type UserView struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Subtitle string   `json:"subtitle,omitempty"`
	Hue      *float64 `json:"hue,omitempty"`
}

type MemberView struct {
	User     UserView  `json:"user"`
	JoinedAt time.Time `json:"joinedAt"`
	RoleIDs  []string  `json:"roleIds"`
}

func memberView(m Member) MemberView {
	roles := make([]string, len(m.RoleIDs))
	for i, id := range m.RoleIDs {
		roles[i] = ids.Format(id)
	}
	return MemberView{
		User:     UserView{ID: ids.Format(m.UserID), Username: m.Username, Name: m.Name, Status: "offline", Subtitle: m.Subtitle, Hue: m.Hue},
		JoinedAt: m.JoinedAt,
		RoleIDs:  roles,
	}
}

type RoleView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Permissions uint64 `json:"permissions"`
	Position    int    `json:"position"`
	Everyone    bool   `json:"everyone"`
}

func roleView(r Role) RoleView {
	return RoleView{ID: ids.Format(r.ID), Name: r.Name, Permissions: uint64(r.Permissions), Position: r.Position, Everyone: r.Everyone}
}

type InviteView struct {
	Code      string     `json:"code"`
	GuildID   string     `json:"guildId"`
	CreatorID string     `json:"creatorId"`
	MaxUses   *int       `json:"maxUses,omitempty"`
	Uses      int        `json:"uses"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

func inviteView(i Invite) InviteView {
	return InviteView{Code: i.Code, GuildID: ids.Format(i.GuildID), CreatorID: ids.Format(i.CreatorID),
		MaxUses: i.MaxUses, Uses: i.Uses, ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
}

type BanView struct {
	User      UserView  `json:"user"`
	BannedBy  string    `json:"bannedBy"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

// Mount registra as rotas de guild. auth é o middleware que exige o usuário autenticado.
func (s *Service) Mount(mux *http.ServeMux, auth func(httpx.Handler) httpx.Handler) {
	h := func(pattern string, fn httpx.Handler) { mux.HandleFunc(pattern, httpx.Wrap(auth(fn))) }
	h("POST /v1/guilds", s.handleCreate)
	h("GET /v1/guilds", s.handleMine)
	h("GET /v1/guilds/{guildId}", s.handleGet)
	h("DELETE /v1/guilds/{guildId}", s.handleDelete)
	h("PUT /v1/guilds/{guildId}/owner", s.handleTransfer)

	h("GET /v1/guilds/{guildId}/members", s.handleMembers)
	h("DELETE /v1/guilds/{guildId}/members/@me", s.handleLeave)
	h("DELETE /v1/guilds/{guildId}/members/{userId}", s.handleKick)
	h("PUT /v1/guilds/{guildId}/members/{userId}/roles/{roleId}", s.handleGrantRole)
	h("DELETE /v1/guilds/{guildId}/members/{userId}/roles/{roleId}", s.handleRevokeRole)

	h("GET /v1/guilds/{guildId}/bans", s.handleBans)
	h("PUT /v1/guilds/{guildId}/bans/{userId}", s.handleBan)
	h("DELETE /v1/guilds/{guildId}/bans/{userId}", s.handleUnban)

	h("GET /v1/guilds/{guildId}/roles", s.handleRoles)
	h("POST /v1/guilds/{guildId}/roles", s.handleCreateRole)
	h("PATCH /v1/guilds/{guildId}/roles/{roleId}", s.handleUpdateRole)
	h("DELETE /v1/guilds/{guildId}/roles/{roleId}", s.handleDeleteRole)

	h("GET /v1/guilds/{guildId}/invites", s.handleInvites)
	h("POST /v1/guilds/{guildId}/invites", s.handleCreateInvite)
	h("GET /v1/invites/{code}", s.handlePreview)
	h("POST /v1/invites/{code}/join", s.handleJoin)
	h("DELETE /v1/invites/{code}", s.handleRevokeInvite)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	g, err := s.Create(r.Context(), user, req.Name)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, guildView(g))
	return nil
}

func (s *Service) handleMine(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	gs, err := s.Mine(r.Context(), user)
	if err != nil {
		return err
	}
	items := make([]GuildView, len(gs))
	for i, g := range gs {
		items[i] = guildView(g)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// guildAndUser lê o usuário e o {guildId}.
func guildAndUser(r *http.Request) (user, guild int64, err error) {
	if user, err = httpx.Caller(r); err != nil {
		return
	}
	guild, err = httpx.PathID(r, "guildId")
	return
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	g, err := s.Get(r.Context(), user, guild)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, guildView(g))
	return nil
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	if err := s.Delete(r.Context(), user, guild); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleTransfer(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	var req struct {
		UserID string `json:"userId"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	target, err := ids.Parse(req.UserID)
	if err != nil {
		return httpx.Validation("userId is invalid")
	}
	if err := s.TransferOwnership(r.Context(), user, guild, target); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleMembers(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", 50, 1, 100)
	if err != nil {
		return err
	}
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		if after, err = ids.Parse(raw); err != nil {
			return httpx.Validation("after is invalid")
		}
	}
	ms, total, err := s.Members(r.Context(), user, guild, after, limit)
	if err != nil {
		return err
	}
	items := make([]MemberView, len(ms))
	for i, m := range ms {
		items[i] = memberView(m)
	}
	out := map[string]any{"items": items, "total": total}
	if len(ms) == limit {
		out["next"] = ids.Format(ms[len(ms)-1].UserID)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) handleLeave(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	if err := s.Leave(r.Context(), user, guild); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleKick(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	target, err := httpx.PathID(r, "userId")
	if err != nil {
		return err
	}
	if err := s.Kick(r.Context(), user, guild, target); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) memberRole(w http.ResponseWriter, r *http.Request, grant bool) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	target, err := httpx.PathID(r, "userId")
	if err != nil {
		return err
	}
	role, err := httpx.PathID(r, "roleId")
	if err != nil {
		return err
	}
	if err := s.SetMemberRole(r.Context(), user, guild, target, role, grant); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleGrantRole(w http.ResponseWriter, r *http.Request) error {
	return s.memberRole(w, r, true)
}

func (s *Service) handleRevokeRole(w http.ResponseWriter, r *http.Request) error {
	return s.memberRole(w, r, false)
}

func (s *Service) handleBans(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	bs, err := s.Bans(r.Context(), user, guild)
	if err != nil {
		return err
	}
	items := make([]BanView, len(bs))
	for i, b := range bs {
		items[i] = BanView{
			User:     UserView{ID: ids.Format(b.UserID), Username: b.Username, Name: b.Name, Status: "offline"},
			BannedBy: ids.Format(b.BannedBy), Reason: b.Reason, CreatedAt: b.CreatedAt,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (s *Service) handleBan(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	target, err := httpx.PathID(r, "userId")
	if err != nil {
		return err
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &req); err != nil {
			return err
		}
	}
	if err := s.Ban(r.Context(), user, guild, target, req.Reason); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleUnban(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	target, err := httpx.PathID(r, "userId")
	if err != nil {
		return err
	}
	if err := s.Unban(r.Context(), user, guild, target); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleRoles(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	rs, err := s.Roles(r.Context(), user, guild)
	if err != nil {
		return err
	}
	items := make([]RoleView, len(rs))
	for i, ro := range rs {
		items[i] = roleView(ro)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (s *Service) handleCreateRole(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	var in RoleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Position = nil // papéis novos entram na posição 1; reordene com PATCH
	role, err := s.CreateRole(r.Context(), user, guild, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, roleView(role))
	return nil
}

func (s *Service) handleUpdateRole(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	roleID, err := httpx.PathID(r, "roleId")
	if err != nil {
		return err
	}
	var in RoleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	role, err := s.UpdateRole(r.Context(), user, guild, roleID, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, roleView(role))
	return nil
}

func (s *Service) handleDeleteRole(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	roleID, err := httpx.PathID(r, "roleId")
	if err != nil {
		return err
	}
	if err := s.DeleteRole(r.Context(), user, guild, roleID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleInvites(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	is, err := s.Invites(r.Context(), user, guild)
	if err != nil {
		return err
	}
	items := make([]InviteView, len(is))
	for i, inv := range is {
		items[i] = inviteView(inv)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (s *Service) handleCreateInvite(w http.ResponseWriter, r *http.Request) error {
	user, guild, err := guildAndUser(r)
	if err != nil {
		return err
	}
	var in InviteInput
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
	}
	inv, err := s.CreateInvite(r.Context(), user, guild, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, inviteView(inv))
	return nil
}

func (s *Service) handlePreview(w http.ResponseWriter, r *http.Request) error {
	if _, err := httpx.Caller(r); err != nil {
		return err
	}
	if httpx.NoNUL(r.PathValue("code")) != nil {
		return httpx.ErrNotFound
	}
	inv, g, err := s.Preview(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": inv.Code, "expiresAt": inv.ExpiresAt, "guild": guildView(g)})
	return nil
}

func (s *Service) handleJoin(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	if httpx.NoNUL(r.PathValue("code")) != nil {
		return httpx.ErrNotFound
	}
	g, err := s.Join(r.Context(), user, r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, guildView(g))
	return nil
}

func (s *Service) handleRevokeInvite(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	if httpx.NoNUL(r.PathValue("code")) != nil {
		return httpx.ErrNotFound
	}
	if err := s.RevokeInvite(r.Context(), user, r.PathValue("code")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
