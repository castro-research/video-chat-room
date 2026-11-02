package gateway

import (
	"context"
	"encoding/json"
	"log/slog"

	"kurz/server/internal/bus"
	"kurz/server/internal/platform/ids"
)

// channelScoped diz se o evento só vai a quem vê o canal. channel.delete não entra: o canal já não
// existe para consultar permissão, então vai a toda a guild (o cliente só descarta o id).
func channelScoped(e bus.Event) bool {
	if e.ChannelID == 0 {
		return false
	}
	switch e.Type {
	case bus.ChannelDelete:
		return false
	}
	return true
}

type routeMeta struct {
	UserID string `json:"userId"`
	Nonce  string `json:"nonce"`
	Author struct {
		ID string `json:"id"`
	} `json:"author"`
}

// route entrega um evento aos sessões certas. Roda numa goroutine só, na ordem em que o bus entrega;
// por isso a invalidação do cache (feita antes de rotear) sempre precede os eventos seguintes.
func (g *Gateway) route(ctx context.Context, e bus.Event) {
	var meta routeMeta
	_ = json.Unmarshal(e.Payload, &meta)
	subject, _ := ids.Parse(meta.UserID)

	if bus.InvalidatesPermissions(e.Type) {
		g.cache.invalidateGuild(e.GuildID)
	}
	sessions := g.snapshot()

	// Membership da sessão: quem entra na guild passa a receber os eventos dela antes do próprio evento.
	switch e.Type {
	case bus.GuildCreate:
		for _, s := range sessions {
			if s.userID == subject {
				s.addGuild(e.GuildID)
				g.send(s, e.Type, e.Payload)
			}
		}
		return
	case bus.MemberJoin:
		for _, s := range sessions {
			if s.userID == subject {
				s.addGuild(e.GuildID)
			}
		}
	}

	// message.create: o nonce volta só ao autor.
	full, stripped := e.Payload, e.Payload
	var authorID int64
	if e.Type == bus.MessageCreate && meta.Nonce != "" {
		authorID, _ = ids.Parse(meta.Author.ID)
		stripped = withoutNonce(e.Payload)
	}

	scoped := channelScoped(e)
	verdict := map[int64]bool{} // por usuário, para não consultar duas vezes (sessões do mesmo usuário)
	for _, s := range sessions {
		if !s.inGuild(e.GuildID) {
			continue
		}
		if scoped {
			ok, seen := verdict[s.userID]
			if !seen {
				var err error
				if ok, err = g.canView(ctx, e.GuildID, s.userID, e.ChannelID); err != nil {
					slog.Error("gateway: permission lookup", "user", s.userID, "channel", e.ChannelID, "err", err)
					ok = false // na dúvida, não entrega
				}
				verdict[s.userID] = ok
			}
			if !ok {
				continue
			}
		}
		if authorID != 0 && s.userID == authorID {
			g.send(s, e.Type, full)
		} else {
			g.send(s, e.Type, stripped)
		}
	}

	switch e.Type {
	case bus.GuildDelete:
		for _, s := range sessions {
			s.removeGuild(e.GuildID)
		}
	case bus.MemberLeave:
		for _, s := range sessions {
			if s.userID == subject {
				s.removeGuild(e.GuildID)
			}
		}
	}
}

func (g *Gateway) send(s *session, typ string, d json.RawMessage) {
	if slow := s.deliver(typ, d); slow != nil {
		g.dropped.Add(1)
		g.slow(slow)
		return
	}
	g.delivered.Add(1)
}

func (g *Gateway) canView(ctx context.Context, guild, user, channel int64) (bool, error) {
	if ok, hit := g.cache.get(guild, user, channel); hit {
		return ok, nil
	}
	ok, err := g.dir.CanView(ctx, user, channel)
	if err != nil {
		return false, err
	}
	g.cache.put(guild, user, channel, ok)
	return ok, nil
}

func withoutNonce(payload json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(payload, &m) != nil {
		return payload
	}
	delete(m, "nonce")
	b, err := json.Marshal(m)
	if err != nil {
		return payload
	}
	return b
}
