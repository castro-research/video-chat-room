package voice

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sfuClaims e sfuAuth reproduzem, linha a linha, experimentos/sfu/sfu.go (struct claims e server.auth).
// TestSFUSourceStillMatches trava a cópia: se o SFU mudar o que valida, o teste quebra aqui.
type sfuClaims struct {
	Name string `json:"name"`
	Room string `json:"room"`
	jwt.RegisteredClaims
}

func sfuAuth(secret []byte, token string, used map[string]bool) (*sfuClaims, error) {
	c := &sfuClaims{}
	_, err := jwt.ParseWithClaims(token, c, func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kurz-manager"), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if c.Subject == "" || c.Room == "" || c.ID == "" {
		return nil, jwt.ErrTokenInvalidClaims
	}
	if used[c.ID] {
		return nil, jwt.ErrTokenInvalidId
	}
	used[c.ID] = true
	return c, nil
}

var secret = []byte("segredo-compartilhado-0123456789")

func TestTokenIsAcceptedByTheSFUValidation(t *testing.T) {
	now := time.Now()
	tok, err := IssueToken(secret, now, "42", "Alice", "777", true)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	c, err := sfuAuth(secret, tok, used)
	if err != nil {
		t.Fatalf("SFU would reject: %v", err)
	}
	if c.Subject != "42" || c.Name != "Alice" || c.Room != "777" || c.Issuer != "kurz-manager" || len(c.ID) != 32 {
		t.Fatalf("claims = %+v", c)
	}
	if d := c.ExpiresAt.Sub(now); d < 59*time.Second || d > 61*time.Second {
		t.Fatalf("exp in %v, want 60s", d)
	}
	// uso único: o SFU guarda o jti
	if _, err := sfuAuth(secret, tok, used); err == nil {
		t.Fatal("second use must be rejected")
	}
}

func TestTokenRejectedWithWrongSecretOrWhenExpired(t *testing.T) {
	tok, _ := IssueToken(secret, time.Now(), "1", "A", "2", true)
	if _, err := sfuAuth([]byte("outro-segredo-0123456789"), tok, map[string]bool{}); err == nil {
		t.Fatal("wrong secret accepted")
	}
	old, _ := IssueToken(secret, time.Now().Add(-2*time.Minute), "1", "A", "2", true)
	if _, err := sfuAuth(secret, old, map[string]bool{}); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestTokensHaveDistinctJTI(t *testing.T) {
	a, _ := IssueToken(secret, time.Now(), "1", "A", "2", true)
	b, _ := IssueToken(secret, time.Now(), "1", "A", "2", true)
	used := map[string]bool{}
	if _, err := sfuAuth(secret, a, used); err != nil {
		t.Fatal(err)
	}
	if _, err := sfuAuth(secret, b, used); err != nil {
		t.Fatalf("second token (new jti) rejected: %v", err)
	}
}

func TestSFUSourceStillMatches(t *testing.T) {
	b, err := os.ReadFile("../../../experimentos/sfu/sfu.go")
	if err != nil {
		t.Fatalf("experimentos/sfu not available: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`jwt.WithValidMethods([]string{"HS256"})`, `jwt.WithIssuer("kurz-manager")`, `jwt.WithExpirationRequired()`,
		`c.Subject == "" || c.Room == "" || c.ID == ""`, `s.jtis[c.ID] = c.ExpiresAt.Time`,
		"Name string `json:\"name\"`", "Room string `json:\"room\"`",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("experimentos/sfu/sfu.go no longer contains %q: update voice/token.go and this test", want)
		}
	}
}
