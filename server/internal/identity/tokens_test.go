package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte(strings.Repeat("k", 32))

func TestAccessTokenRoundTrip(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	raw, err := iss.Issue(12345)
	if err != nil {
		t.Fatal(err)
	}
	id, err := iss.Parse(raw)
	if err != nil || id != 12345 {
		t.Fatalf("id = %d, err = %v", id, err)
	}
}

func TestAccessTokenExpires(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	now := time.Now()
	iss.now = func() time.Time { return now }
	raw, _ := iss.Issue(1)
	iss.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := iss.Parse(raw); err != ErrTokenExpired {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestAccessTokenRejectsForgeries(t *testing.T) {
	iss := NewTokenIssuer(testSecret, time.Minute)
	other := NewTokenIssuer([]byte(strings.Repeat("z", 32)), time.Minute)
	foreign, _ := other.Issue(1)
	valid, _ := iss.Issue(1)

	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Issuer: "kurz", Subject: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))})
	noneRaw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	for name, raw := range map[string]string{
		"wrong secret": foreign,
		"tampered":     valid[:len(valid)-2] + "xx",
		"alg none":     noneRaw,
		"garbage":      "not.a.jwt",
		"empty":        "",
	} {
		if _, err := iss.Parse(raw); err != ErrTokenInvalid {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", name, err)
		}
	}
}

func TestRefreshTokensAreRandomAndHashStably(t *testing.T) {
	a, ha := newRefreshToken()
	b, hb := newRefreshToken()
	if a == b || len(a) < 40 {
		t.Fatalf("tokens not random enough: %q %q", a, b)
	}
	if string(ha) == string(hb) {
		t.Fatal("different tokens produced the same hash")
	}
	if string(hashRefreshToken(a)) != string(ha) {
		t.Fatal("hashRefreshToken does not match the hash returned with the token")
	}
}
