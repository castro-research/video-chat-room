package voice

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Formato do token de mídia: o MESMO do manager de experimentos/manager (e validado por experimentos/sfu/sfu.go):
// JWT HS256, iss="kurz-manager", sub=id do usuário, name, room=id do canal de voz, jti aleatório, exp = agora + 60 s.
// O SFU rejeita assinatura inválida, expirado ou jti já usado (guarda os jtis até o exp, em memória): uso único.
// O claim extra `speak` é informativo: o SFU atual ignora claims que não conhece e NÃO impede ninguém de publicar
// áudio, então a permissão Speak é aplicada aqui (estado `muted` forçado), não no SFU.
const (
	Issuer   = "kurz-manager"
	TokenTTL = 60 * time.Second
)

type claims struct {
	Name  string `json:"name"`
	Room  string `json:"room"`
	Speak bool   `json:"speak"`
	jwt.RegisteredClaims
}

// IssueToken assina o token de mídia. user e room são ids já formatados (ids.Format).
func IssueToken(secret []byte, now time.Time, user, name, room string, speak bool) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Name: name, Room: room, Speak: speak,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: Issuer, Subject: user, ID: hex.EncodeToString(jti),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
		},
	}).SignedString(secret)
}
