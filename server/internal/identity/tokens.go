package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"kurz/server/internal/platform/ids"
)

var (
	ErrTokenExpired = errors.New("identity: token expired")
	ErrTokenInvalid = errors.New("identity: token invalid")
)

const tokenIssuer = "kurz"

// TokenIssuer emite e valida o access token (JWT HS256, curto).
type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokenIssuer(secret []byte, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, ttl: ttl, now: time.Now}
}

func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

func (t *TokenIssuer) Issue(userID int64) (string, error) {
	now := t.now()
	claims := jwt.RegisteredClaims{
		Issuer:    tokenIssuer,
		Subject:   ids.Format(userID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// Parse devolve o ID do usuário, ErrTokenExpired ou ErrTokenInvalid.
func (t *TokenIssuer) Parse(raw string) (int64, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return 0, ErrTokenExpired
	}
	if err != nil {
		return 0, ErrTokenInvalid
	}
	id, err := ids.Parse(claims.Subject)
	if err != nil {
		return 0, ErrTokenInvalid
	}
	return id, nil
}

// newRefreshToken devolve o token opaco (vai ao cliente) e o hash SHA-256 (vai ao banco).
func newRefreshToken() (raw string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sem entropia não dá para continuar
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashRefreshToken(raw)
}

func hashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
