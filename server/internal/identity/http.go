package identity

import (
	"net/http"
	"strings"

	"kurz/server/internal/platform/httpx"
)

// Require exige `Authorization: Bearer <access token>` e põe o usuário no contexto.
// O esquema não diferencia maiúsculas (RFC 7235); token vazio é rejeitado.
func Require(tokens *TokenIssuer) func(httpx.Handler) httpx.Handler {
	return func(next httpx.Handler) httpx.Handler {
		return func(w http.ResponseWriter, r *http.Request) error {
			raw, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				return httpx.ErrUnauthorized
			}
			id, err := tokens.Parse(raw)
			switch err {
			case nil:
			case ErrTokenExpired:
				return httpx.NewError(http.StatusUnauthorized, "token_expired", "access token expired")
			default:
				return httpx.ErrUnauthorized
			}
			return next(w, r.WithContext(httpx.WithUserID(r.Context(), id)))
		}
	}
}

func bearerToken(header string) (string, bool) {
	scheme, raw, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	raw = strings.TrimLeft(raw, " ")
	return raw, raw != ""
}

// Mount registra as rotas de auth e de perfil.
func (s *Service) Mount(mux *http.ServeMux) {
	auth := Require(s.tokens)
	mux.HandleFunc("POST /v1/auth/register", httpx.Wrap(s.handleRegister))
	mux.HandleFunc("POST /v1/auth/login", httpx.Wrap(s.handleLogin))
	mux.HandleFunc("POST /v1/auth/refresh", httpx.Wrap(s.handleRefresh))
	mux.HandleFunc("POST /v1/auth/logout", httpx.Wrap(s.handleLogout))
	mux.HandleFunc("GET /v1/me", httpx.Wrap(auth(s.handleMe)))
	mux.HandleFunc("PATCH /v1/me", httpx.Wrap(auth(s.handleUpdateMe)))
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) error {
	var req RegisterRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Register(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, sess)
	return nil
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req LoginRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Login(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
	return nil
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) error {
	var req RefreshRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	sess, err := s.Refresh(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sess)
	return nil
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) error {
	var req RefreshRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := s.Logout(r.Context(), req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := httpx.UserID(r.Context())
	u, err := s.Me(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}

func (s *Service) handleUpdateMe(w http.ResponseWriter, r *http.Request) error {
	id, _ := httpx.UserID(r.Context())
	var req UpdateMeRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	u, err := s.UpdateMe(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}
