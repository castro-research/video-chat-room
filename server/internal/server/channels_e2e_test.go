package server

import (
	"testing"

	"kurz/server/internal/permissions"
)

func channelNamed(u *user, gid, name string) map[string]any {
	u.t.Helper()
	for _, c := range items(u.do("GET", "/v1/guilds/"+gid+"/channels", 200, nil)) {
		if c["name"] == name {
			return c
		}
	}
	return nil
}

// siblings devolve os nomes dos canais com o mesmo pai, na ordem de exibição.
func siblings(u *user, gid, parent string) []string {
	u.t.Helper()
	var out []string
	for _, c := range items(u.do("GET", "/v1/guilds/"+gid+"/channels", 200, nil)) {
		p, _ := c["parentId"].(string)
		if p == parent {
			out = append(out, c["name"].(string))
		}
	}
	return out
}

func TestChannelCrudAndOrdering(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	base := "/v1/guilds/" + gid + "/channels"

	// sem ManageChannels bob não cria, edita nem apaga
	geral := channelNamed(bob, gid, "geral")["id"].(string)
	bob.fail("POST", base, 403, "missing_permission", map[string]any{"name": "x", "kind": "text"})
	bob.fail("PATCH", "/v1/channels/"+geral, 403, "missing_permission", map[string]any{"name": "x"})
	bob.fail("DELETE", "/v1/channels/"+geral, 403, "missing_permission", nil)

	// validações
	alice.fail("POST", base, 422, "validation", map[string]any{"name": "x", "kind": "stage"})
	alice.fail("POST", base, 422, "validation", map[string]any{"name": "", "kind": "text"})
	cat := alice.do("POST", base, 201, map[string]any{"name": "Texto", "kind": "category"})["id"].(string)
	alice.fail("POST", base, 422, "validation", map[string]any{"name": "c2", "kind": "category", "parentId": cat})
	alice.fail("POST", base, 422, "validation", map[string]any{"name": "x", "kind": "text", "parentId": geral}) // pai não é categoria

	dev := alice.do("POST", base, 201, map[string]any{"name": "dev", "kind": "text", "parentId": cat})
	if dev["parentId"] != cat {
		t.Fatalf("dev = %v", dev)
	}
	voice := alice.do("POST", base, 201, map[string]any{"name": "Sala", "kind": "voice"})
	if voice["kind"] != "voice" {
		t.Fatalf("voice = %v", voice)
	}

	// categoria de outra guild não serve de pai
	other := createGuild(alice, "Outra")
	otherCat := alice.do("POST", "/v1/guilds/"+other+"/channels", 201, map[string]any{"name": "c", "kind": "category"})["id"].(string)
	alice.fail("POST", base, 422, "validation", map[string]any{"name": "x", "kind": "text", "parentId": otherCat})

	// reordenar irmãos do topo: geral, Texto, Sala -> Sala vai para o começo
	if got := siblings(alice, gid, ""); !eq(got, []string{"geral", "Texto", "Sala"}) {
		t.Fatalf("top = %v", got)
	}
	alice.do("PATCH", "/v1/channels/"+voice["id"].(string), 200, map[string]any{"position": 0})
	if got := siblings(alice, gid, ""); !eq(got, []string{"Sala", "geral", "Texto"}) {
		t.Fatalf("top after move = %v", got)
	}
	// posição além do fim vai para o fim
	alice.do("PATCH", "/v1/channels/"+voice["id"].(string), 200, map[string]any{"position": 99})
	if got := siblings(alice, gid, ""); !eq(got, []string{"geral", "Texto", "Sala"}) {
		t.Fatalf("top after clamp = %v", got)
	}

	// mover geral para a categoria (vai para o fim) e voltar (parentId null)
	alice.do("PATCH", "/v1/channels/"+geral, 200, map[string]any{"parentId": cat})
	if got := siblings(alice, gid, cat); !eq(got, []string{"dev", "geral"}) {
		t.Fatalf("in category = %v", got)
	}
	alice.do("PATCH", "/v1/channels/"+geral, 200, map[string]any{"parentId": nil, "name": "geral2"})
	if c := channelNamed(alice, gid, "geral2"); c == nil || c["parentId"] != nil {
		t.Fatalf("geral2 = %v", c)
	}
	alice.fail("PATCH", "/v1/channels/"+cat, 422, "validation", map[string]any{"parentId": cat})

	// apagar a categoria solta os filhos
	alice.do("DELETE", "/v1/channels/"+cat, 204, nil)
	if c := channelNamed(alice, gid, "dev"); c == nil || c["parentId"] != nil {
		t.Fatalf("dev after category delete = %v", c)
	}
	alice.fail("GET", "/v1/channels/"+cat, 404, "not_found", nil)

	// quem não é membro da guild não toca no canal
	carol := newUser(t, srv, "carol")
	carol.fail("GET", "/v1/channels/"+geral, 404, "not_found", nil)
	carol.fail("DELETE", "/v1/channels/"+geral, 404, "not_found", nil)
	carol.fail("GET", "/v1/guilds/"+gid+"/channels", 404, "not_found", nil)
}

func TestChannelOverridesE2E(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	gbase := "/v1/guilds/" + gid

	staff := alice.do("POST", gbase+"/channels", 201, map[string]any{"name": "staff", "kind": "text"})["id"].(string)
	cbase := "/v1/channels/" + staff
	ov := func(kind, target string) string { return cbase + "/overrides/" + kind + "/" + target }

	// canal privado: @everyone não vê
	alice.do("PUT", ov("role", gid), 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	if channelNamed(bob, gid, "staff") != nil {
		t.Fatal("bob should not list the hidden channel")
	}
	bob.fail("GET", cbase, 404, "not_found", nil)
	if channelNamed(alice, gid, "staff") == nil {
		t.Fatal("owner still sees it")
	}

	// papel "boss" (criado antes) fica acima de "staffer"
	boss := alice.do("POST", gbase+"/roles", 201, map[string]any{"name": "boss"})["id"].(string)
	staffer := alice.do("POST", gbase+"/roles", 201, map[string]any{"name": "staffer", "permissions": perm(permissions.ManageChannels)})["id"].(string)
	alice.do("PUT", ov("role", staffer), 200, map[string]any{"allow": perm(permissions.ViewChannel)})
	alice.do("PUT", gbase+"/members/"+bob.id+"/roles/"+staffer, 204, nil)

	if channelNamed(bob, gid, "staff") == nil {
		t.Fatal("staffer should see the channel")
	}
	bob.do("GET", cbase, 200, nil)
	if channelNamed(carol, gid, "staff") != nil {
		t.Fatal("carol still must not see it")
	}

	// overrides: sem ManageChannels, nada
	carol.fail("GET", cbase+"/overrides", 404, "not_found", nil) // nem enxerga o canal
	if got := items(bob.do("GET", cbase+"/overrides", 200, nil)); len(got) != 2 {
		t.Fatalf("overrides = %v", got)
	}

	// bob (staffer) mexe no que está abaixo dele
	bob.do("PUT", ov("member", carol.id), 200, map[string]any{"allow": perm(permissions.ViewChannel)})
	if channelNamed(carol, gid, "staff") == nil {
		t.Fatal("member override should reveal the channel to carol")
	}
	bob.do("PUT", ov("member", carol.id), 200, map[string]any{"allow": perm(permissions.ViewChannel), "deny": perm(permissions.SendMessages)})
	// sem allow/deny o override some
	bob.do("PUT", ov("member", carol.id), 200, map[string]any{})
	if channelNamed(carol, gid, "staff") != nil {
		t.Fatal("empty override should remove it")
	}
	bob.do("PUT", ov("member", carol.id), 200, map[string]any{"allow": perm(permissions.ViewChannel)})
	bob.do("DELETE", ov("member", carol.id), 204, nil)
	if channelNamed(carol, gid, "staff") != nil {
		t.Fatal("deleted override should hide it again")
	}

	// limites do bob
	bob.fail("PUT", ov("role", boss), 403, "hierarchy", map[string]any{"deny": perm(permissions.SendMessages)})
	bob.fail("PUT", ov("member", alice.id), 403, "hierarchy", map[string]any{"deny": perm(permissions.SendMessages)})
	bob.fail("PUT", ov("member", carol.id), 403, "missing_permission", map[string]any{"allow": perm(permissions.ManageMessages)}) // não tem o bit
	bob.fail("PUT", ov("member", carol.id), 422, "validation", map[string]any{"allow": perm(permissions.ViewChannel), "deny": perm(permissions.ViewChannel)})
	bob.fail("PUT", ov("member", carol.id), 422, "validation", map[string]any{"deny": perm(permissions.Administrator)})
	bob.fail("PUT", ov("member", carol.id), 422, "validation", map[string]any{"deny": 1 << 50})
	bob.fail("PUT", ov("thing", carol.id), 404, "not_found", map[string]any{})
	bob.fail("PUT", ov("role", "999"), 404, "not_found", map[string]any{"deny": 1})
	// bob pode mexer no próprio override (sem escalar)
	bob.do("PUT", ov("member", bob.id), 200, map[string]any{"deny": perm(permissions.SendMessages)})
	// sem ManageChannels no canal (deny do dono), bob perde o poder
	alice.do("PUT", ov("member", bob.id), 200, map[string]any{"allow": perm(permissions.ViewChannel), "deny": perm(permissions.ManageChannels)})
	bob.fail("PATCH", cbase, 403, "missing_permission", map[string]any{"name": "x"})
	bob.fail("PUT", ov("member", carol.id), 403, "missing_permission", map[string]any{})
	bob.fail("DELETE", cbase, 403, "missing_permission", nil)
}
