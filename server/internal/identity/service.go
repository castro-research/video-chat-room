package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

type Config struct {
	Registration string // "open" ou "closed"
	RefreshTTL   time.Duration
}

type Service struct {
	store     *store
	ids       *ids.Generator
	tokens    *TokenIssuer
	cfg       Config
	now       func() time.Time
	dummyHash string // para gastar o mesmo tempo quando o usuário não existe
}

func NewService(db *pgxpool.Pool, gen *ids.Generator, tokens *TokenIssuer, cfg Config) *Service {
	dummy, err := HashPassword("kurz-dummy-password")
	if err != nil {
		panic(err)
	}
	return &Service{store: &store{db: db}, ids: gen, tokens: tokens, cfg: cfg, now: time.Now, dummyHash: dummy}
}

// Tipos de requisição e resposta (JSON do contrato).

type RegisterRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Name       string `json:"name"`
	InviteCode string `json:"inviteCode"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type UpdateMeRequest struct {
	Name     *string  `json:"name"`
	Subtitle *string  `json:"subtitle"`
	Hue      *float64 `json:"hue"`
}

// UserView é o User da API. `status` é sempre offline até o gateway existir (Plano 4).
type UserView struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Subtitle string   `json:"subtitle,omitempty"`
	Hue      *float64 `json:"hue,omitempty"`
}

type Session struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	ExpiresIn    int      `json:"expiresIn"`
	User         UserView `json:"user"`
}

func view(u User) UserView {
	return UserView{ID: ids.Format(u.ID), Username: u.Username, Name: u.Name, Status: "offline", Subtitle: u.Subtitle, Hue: u.Hue}
}

var errBadCredentials = httpx.NewError(401, "unauthorized", "invalid username or password")

func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (s *Service) Register(ctx context.Context, req RegisterRequest) (Session, error) {
	if s.cfg.Registration == "closed" {
		return Session{}, httpx.NewError(403, "registration_closed", "registration is closed")
	}
	username := normalizeUsername(req.Username)
	if !usernameRe.MatchString(username) {
		return Session{}, httpx.Validation("username must be 3-32 characters of a-z, 0-9 or _")
	}
	if n := utf8.RuneCountInString(req.Password); n < 8 || n > 128 {
		return Session{}, httpx.Validation("password must have 8-128 characters")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = username
	}
	if n := utf8.RuneCountInString(name); n > 64 {
		return Session{}, httpx.Validation("name must have 1-64 characters")
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.createUser(ctx, s.ids.Next(), username, name, hash)
	if errors.Is(err, errUsernameTaken) {
		return Session{}, httpx.NewError(409, "username_taken", "username is already taken")
	}
	if err != nil {
		return Session{}, err
	}
	return s.newSession(ctx, user, s.ids.Next())
}

func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, error) {
	user, hash, err := s.store.userByUsername(ctx, normalizeUsername(req.Username))
	if errors.Is(err, errNotFound) {
		VerifyPassword(req.Password, s.dummyHash)
		return Session{}, errBadCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if !VerifyPassword(req.Password, hash) {
		return Session{}, errBadCredentials
	}
	return s.newSession(ctx, user, s.ids.Next())
}

func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (Session, error) {
	raw, hash := newRefreshToken()
	now := s.now()
	userID, err := s.store.rotateRefresh(ctx, hashRefreshToken(req.RefreshToken),
		newRefresh{ID: s.ids.Next(), Hash: hash, ExpiresAt: now.Add(s.cfg.RefreshTTL)}, now, refreshReuseGrace)
	if errors.Is(err, errInvalidRefresh) || errors.Is(err, errRefreshReused) {
		return Session{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.userByID(ctx, userID)
	if errors.Is(err, errNotFound) {
		return Session{}, httpx.ErrUnauthorized // conta apagada entre a rotação e a leitura
	}
	if err != nil {
		return Session{}, err
	}
	return s.sessionFor(user, raw)
}

func (s *Service) Logout(ctx context.Context, req RefreshRequest) error {
	return s.store.revokeFamily(ctx, hashRefreshToken(req.RefreshToken), s.now())
}

func (s *Service) Me(ctx context.Context, id int64) (UserView, error) {
	u, err := s.store.userByID(ctx, id)
	if errors.Is(err, errNotFound) {
		return UserView{}, httpx.ErrUnauthorized // conta apagada com token ainda válido
	}
	if err != nil {
		return UserView{}, err
	}
	return view(u), nil
}

func (s *Service) UpdateMe(ctx context.Context, id int64, req UpdateMeRequest) (UserView, error) {
	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(*req.Name); n < 1 || n > 64 {
			return UserView{}, httpx.Validation("name must have 1-64 characters")
		}
	}
	if req.Subtitle != nil && utf8.RuneCountInString(*req.Subtitle) > 128 {
		return UserView{}, httpx.Validation("subtitle must have at most 128 characters")
	}
	if req.Hue != nil && (*req.Hue < 0 || *req.Hue > 360) {
		return UserView{}, httpx.Validation("hue must be between 0 and 360")
	}
	u, err := s.store.updateProfile(ctx, id, req.Name, req.Subtitle, req.Hue)
	if errors.Is(err, errNotFound) {
		return UserView{}, httpx.ErrUnauthorized
	}
	if err != nil {
		return UserView{}, err
	}
	return view(u), nil
}

// newSession abre uma família de refresh tokens nova (login ou registro).
func (s *Service) newSession(ctx context.Context, user User, familyID int64) (Session, error) {
	raw, hash := newRefreshToken()
	err := s.store.insertRefresh(ctx, newRefresh{
		ID: s.ids.Next(), UserID: user.ID, FamilyID: familyID, Hash: hash, ExpiresAt: s.now().Add(s.cfg.RefreshTTL),
	})
	if err != nil {
		return Session{}, err
	}
	return s.sessionFor(user, raw)
}

func (s *Service) sessionFor(user User, refreshRaw string) (Session, error) {
	access, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: refreshRaw, ExpiresIn: int(s.tokens.TTL().Seconds()), User: view(user)}, nil
}
