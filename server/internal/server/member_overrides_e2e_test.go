package server

import (
	"testing"

	"kurz/server/internal/permissions"
)

// Overrides por membro não podem sobreviver a leave/kick/ban: a reentrada por convite (ou após unban) volta sem acesso.
func TestMemberOverridesDieWithMembership(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	gbase := "/v1/guilds/" + gid
	code := invite(alice, gid, nil)

	secret := alice.do("POST", gbase+"/channels", 201, map[string]any{"name": "secreto", "kind": "text"})["id"].(string)
	alice.do("PUT", "/v1/channels/"+secret+"/overrides/role/"+gid, 200, map[string]any{"deny": perm(permissions.ViewChannel)})

	grant := func() {
		alice.do("PUT", "/v1/channels/"+secret+"/overrides/member/"+bob.id, 200, map[string]any{"allow": perm(permissions.ViewChannel)})
		if channelNamed(bob, gid, "secreto") == nil {
			t.Fatal("bob deveria ver o canal com o override")
		}
	}
	rejoin := func(step string) {
		bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
		if channelNamed(bob, gid, "secreto") != nil {
			t.Fatalf("%s: o override de membro sobreviveu", step)
		}
		bob.fail("GET", "/v1/channels/"+secret, 404, "not_found", nil)
		for _, o := range items(alice.do("GET", "/v1/channels/"+secret+"/overrides", 200, nil)) {
			if o["targetKind"] == "member" {
				t.Fatalf("%s: override órfão %v", step, o)
			}
		}
	}

	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)

	grant()
	bob.do("DELETE", gbase+"/members/@me", 204, nil)
	rejoin("leave")

	grant()
	alice.do("DELETE", gbase+"/members/"+bob.id, 204, nil)
	rejoin("kick")

	grant()
	alice.do("PUT", gbase+"/bans/"+bob.id, 204, nil)
	alice.do("DELETE", gbase+"/bans/"+bob.id, 204, nil)
	rejoin("ban+unban")
}
