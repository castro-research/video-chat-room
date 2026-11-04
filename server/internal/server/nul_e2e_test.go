package server

import (
	"net/url"
	"testing"
)

// NUL em qualquer texto vira 422 validation (o Postgres recusaria com 22021 e o resultado seria 500).
func TestNULInTextIsValidationNot500(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	base := "/v1/channels/" + ch + "/messages"
	mid := alice.do("POST", base, 201, map[string]any{"text": "ok"})["id"].(string)
	nul := "a\x00b"

	alice.fail("POST", base, 422, "validation", map[string]any{"text": nul})
	alice.fail("PATCH", base+"/"+mid, 422, "validation", map[string]any{"text": nul})
	alice.fail("POST", base, 422, "validation", map[string]any{"text": "ok", "nonce": nul})
	alice.fail("POST", "/v1/guilds", 422, "validation", map[string]any{"name": nul})
	alice.fail("POST", "/v1/guilds/"+gid+"/channels", 422, "validation", map[string]any{"name": nul, "kind": "text"})
	alice.fail("PATCH", "/v1/channels/"+ch, 422, "validation", map[string]any{"name": nul})
	alice.fail("POST", "/v1/guilds/"+gid+"/roles", 422, "validation", map[string]any{"name": nul})
	alice.fail("PATCH", "/v1/me", 422, "validation", map[string]any{"name": nul})
	alice.fail("PATCH", "/v1/me", 422, "validation", map[string]any{"subtitle": nul})
	alice.fail("PUT", "/v1/guilds/"+gid+"/bans/"+bob.id, 422, "validation", map[string]any{"reason": nul})
	alice.fail("PUT", base+"/"+mid+"/reactions/"+url.PathEscape(nul)+"/@me", 422, "validation", nil)
	alice.fail("GET", "/v1/guilds/"+gid+"/search?q="+url.QueryEscape(nul), 422, "validation", nil)
	alice.fail("GET", "/v1/invites/"+url.PathEscape(nul), 404, "not_found", nil)
	alice.fail("POST", "/v1/invites/"+url.PathEscape(nul)+"/join", 404, "not_found", nil)

	for _, p := range []string{"register", "login"} {
		status, out := call(t, "POST", srv+"/v1/auth/"+p, "", map[string]string{"username": "x" + nul, "password": "password123"})
		if status != 422 || out["code"] != "validation" {
			t.Fatalf("%s with NUL username = %d %v", p, status, out)
		}
		status, out = call(t, "POST", srv+"/v1/auth/"+p, "", map[string]string{"username": "xyz", "password": "password" + nul})
		if status != 422 {
			t.Fatalf("%s with NUL password = %d %v", p, status, out)
		}
	}
	// nada disso quebrou o resto
	alice.do("POST", base, 201, map[string]any{"text": "ainda funciona"})
}
