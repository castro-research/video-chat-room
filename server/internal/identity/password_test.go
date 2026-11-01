package identity

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected format: %s", h)
	}
	if !VerifyPassword("correct horse", h) {
		t.Fatal("right password rejected")
	}
	if VerifyPassword("wrong horse", h) {
		t.Fatal("wrong password accepted")
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,t=1,p=1$a", "$bcrypt$v=19$m=1,t=1,p=1$YQ$YQ", "$argon2id$v=19$m=x,t=1,p=1$YQ$YQ"} {
		if VerifyPassword("x", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}
