package voice

import (
	"net/http"

	"kurz/server/internal/platform/httpx"
)

func (s *Service) Mount(mux *http.ServeMux, auth func(httpx.Handler) httpx.Handler) {
	h := func(pattern string, fn httpx.Handler) { mux.HandleFunc(pattern, httpx.Wrap(auth(fn))) }
	h("POST /v1/channels/{channelId}/voice/join", s.handleJoin)
	h("POST /v1/channels/{channelId}/voice/leave", s.handleLeave)
	h("GET /v1/channels/{channelId}/voice", s.handleParticipants)
}

func (s *Service) handleJoin(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	ch, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	v, err := s.Join(r.Context(), user, ch)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, v)
	return nil
}

func (s *Service) handleLeave(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	ch, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	if err := s.Leave(r.Context(), user, ch); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleParticipants(w http.ResponseWriter, r *http.Request) error {
	user, err := httpx.Caller(r)
	if err != nil {
		return err
	}
	ch, err := httpx.PathID(r, "channelId")
	if err != nil {
		return err
	}
	ps, err := s.Participants(r.Context(), user, ch)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"participants": ps})
	return nil
}
