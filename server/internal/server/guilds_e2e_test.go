package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kurz/server/internal/permissions"
)

// user é um usuário de teste já registrado.
type user struct {
	t     *testing.T
	base  string
	token string
	id    string
}

func newUser(t *testing.T, srv string, name string) *user {
	t.Helper()
	status, sess := call(t, "POST", srv+"/v1/auth/register", "", map[string]string{"username": name, "password": "password123"})
	if status != 201 {
		t.Fatalf("register %s = %d %v", name, status, sess)
	}
	return &user{t: t, base: srv, token: sess["accessToken"].(string), id: sess["user"].(map[string]any)["id"].(string)}
}

// do faz a chamada e confere o status esperado.
func (u *user) do(method, path string, want int, body any) map[string]any {
	u.t.Helper()
	status, out := call(u.t, method, u.base+path, u.token, body)
	if status != want {
		u.t.Fatalf("%s %s = %d, queria %d: %v", method, path, status, want, out)
	}
	return out
}

// fail espera um erro com status e code.
func (u *user) fail(method, path string, want int, code string, body any) {
	u.t.Helper()
	out := u.do(method, path, want, body)
	if out["code"] != code {
		u.t.Fatalf("%s %s: code = %v, queria %s", method, path, out["code"], code)
	}
}

func items(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range m["items"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func names(m map[string]any) []string {
	var out []string
	for _, it := range items(m) {
		out = append(out, it["name"].(string))
	}
	return out
}

func eq(a, b []string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func createGuild(owner *user, name string) string {
	return owner.do("POST", "/v1/guilds", 201, map[string]any{"name": name})["id"].(string)
}

func invite(u *user, guild string, body map[string]any) string {
	return u.do("POST", "/v1/guilds/"+guild+"/invites", 201, body)["code"].(string)
}

func perm(ps ...permissions.Perm) uint64 {
	var p permissions.Perm
	for _, x := range ps {
		p |= x
	}
	return uint64(p)
}

func TestGuildMembershipFlow(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol, dave := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol"), newUser(t, srv, "dave")

	if status, _ := call(t, "GET", srv+"/v1/guilds", "", nil); status != 401 {
		t.Fatalf("guilds without token = %d", status)
	}

	// criar guild: dono, @everyone e #geral
	alice.do("POST", "/v1/guilds", 422, map[string]any{"name": "  "})
	gid := createGuild(alice, "Casa")
	g := alice.do("GET", "/v1/guilds/"+gid, 200, nil)
	if g["name"] != "Casa" || g["ownerId"] != alice.id {
		t.Fatalf("guild = %v", g)
	}
	if got := names(alice.do("GET", "/v1/guilds/"+gid+"/channels", 200, nil)); !eq(got, []string{"geral"}) {
		t.Fatalf("channels = %v", got)
	}
	roles := items(alice.do("GET", "/v1/guilds/"+gid+"/roles", 200, nil))
	if len(roles) != 1 || roles[0]["name"] != "@everyone" || roles[0]["everyone"] != true {
		t.Fatalf("roles = %v", roles)
	}
	if got := items(alice.do("GET", "/v1/guilds", 200, nil)); len(got) != 1 {
		t.Fatalf("my guilds = %v", got)
	}

	// quem não é membro não vê a guild (404)
	bob.fail("GET", "/v1/guilds/"+gid, 404, "not_found", nil)
	bob.fail("GET", "/v1/guilds/"+gid+"/members", 404, "not_found", nil)

	// convite de 1 uso
	code := invite(alice, gid, map[string]any{"maxUses": 1})
	if prev := bob.do("GET", "/v1/invites/"+code, 200, nil); prev["guild"].(map[string]any)["name"] != "Casa" {
		t.Fatalf("preview = %v", prev)
	}
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil) // já é membro: idempotente, não gasta uso
	carol.fail("POST", "/v1/invites/"+code+"/join", 410, "invite_expired", nil)
	carol.fail("GET", "/v1/invites/"+code, 410, "invite_expired", nil)
	carol.fail("POST", "/v1/invites/nope1234/join", 404, "invalid_invite", nil)
	if m := alice.do("GET", "/v1/guilds/"+gid+"/members", 200, nil); m["total"] != float64(2) {
		t.Fatalf("members = %v", m)
	}

	// membro comum não modera nem gerencia a guild
	bob.fail("DELETE", "/v1/guilds/"+gid+"/members/"+alice.id, 403, "missing_permission", nil)
	bob.fail("PUT", "/v1/guilds/"+gid+"/bans/"+alice.id, 403, "missing_permission", nil)
	bob.fail("GET", "/v1/guilds/"+gid+"/bans", 403, "missing_permission", nil)
	bob.fail("POST", "/v1/guilds/"+gid+"/roles", 403, "missing_permission", map[string]any{"name": "x"})
	bob.fail("DELETE", "/v1/guilds/"+gid, 403, "forbidden", nil)
	bob.fail("PUT", "/v1/guilds/"+gid+"/owner", 403, "forbidden", map[string]any{"userId": bob.id})
	alice.fail("DELETE", "/v1/guilds/"+gid+"/members/@me", 409, "owner_cannot_leave", nil)

	// convite sem limites; carol e dave entram
	open := invite(alice, gid, nil)
	carol.do("POST", "/v1/invites/"+open+"/join", 200, nil)
	dave.do("POST", "/v1/invites/"+open+"/join", 200, nil)

	// papel "mod" (kick+ban) para o bob
	mod := alice.do("POST", "/v1/guilds/"+gid+"/roles", 201, map[string]any{"name": "mod", "permissions": perm(permissions.KickMembers, permissions.BanMembers)})["id"].(string)
	alice.do("PUT", "/v1/guilds/"+gid+"/members/"+bob.id+"/roles/"+mod, 204, nil)
	found := false
	for _, m := range items(alice.do("GET", "/v1/guilds/"+gid+"/members", 200, nil)) {
		if m["user"].(map[string]any)["id"] == bob.id {
			found = fmt.Sprint(m["roleIds"]) == "["+mod+"]"
		}
	}
	if !found {
		t.Fatal("bob should list the mod role")
	}

	// kick: carol sai, mas pode voltar
	bob.do("DELETE", "/v1/guilds/"+gid+"/members/"+carol.id, 204, nil)
	carol.fail("GET", "/v1/guilds/"+gid, 404, "not_found", nil)
	carol.do("POST", "/v1/invites/"+open+"/join", 200, nil)
	// ninguém expulsa o dono nem a si mesmo
	bob.fail("DELETE", "/v1/guilds/"+gid+"/members/"+alice.id, 403, "hierarchy", nil)
	bob.fail("DELETE", "/v1/guilds/"+gid+"/members/"+bob.id, 422, "validation", nil)

	// hierarquia: helper (abaixo de mod) não expulsa o mod
	helper := alice.do("POST", "/v1/guilds/"+gid+"/roles", 201, map[string]any{"name": "helper", "permissions": perm(permissions.KickMembers)})["id"].(string)
	alice.do("PUT", "/v1/guilds/"+gid+"/members/"+carol.id+"/roles/"+helper, 204, nil)
	carol.fail("DELETE", "/v1/guilds/"+gid+"/members/"+bob.id, 403, "hierarchy", nil)
	bob.do("DELETE", "/v1/guilds/"+gid+"/members/"+carol.id, 204, nil) // mod expulsa helper
	carol.do("POST", "/v1/invites/"+open+"/join", 200, nil)

	// ban: dave não volta até o unban
	bob.do("PUT", "/v1/guilds/"+gid+"/bans/"+dave.id, 204, map[string]any{"reason": "spam"})
	dave.fail("POST", "/v1/invites/"+open+"/join", 403, "banned", nil)
	dave.fail("GET", "/v1/guilds/"+gid, 404, "not_found", nil)
	bans := items(bob.do("GET", "/v1/guilds/"+gid+"/bans", 200, nil))
	if len(bans) != 1 || bans[0]["reason"] != "spam" || bans[0]["user"].(map[string]any)["id"] != dave.id {
		t.Fatalf("bans = %v", bans)
	}
	bob.do("DELETE", "/v1/guilds/"+gid+"/bans/"+dave.id, 204, nil)
	bob.fail("DELETE", "/v1/guilds/"+gid+"/bans/"+dave.id, 404, "not_found", nil)
	dave.do("POST", "/v1/invites/"+open+"/join", 200, nil)
	// ban de quem nem está na guild (usuário existente)
	erin := newUser(t, srv, "erin")
	bob.do("PUT", "/v1/guilds/"+gid+"/bans/"+erin.id, 204, nil)
	erin.fail("POST", "/v1/invites/"+open+"/join", 403, "banned", nil)

	// revogar convite: criador ou admin
	bob.fail("DELETE", "/v1/invites/"+open, 403, "missing_permission", nil)
	alice.do("DELETE", "/v1/invites/"+open, 204, nil)
	erin.fail("GET", "/v1/invites/"+open, 404, "invalid_invite", nil)

	// sair, transferir posse e excluir
	dave.do("DELETE", "/v1/guilds/"+gid+"/members/@me", 204, nil)
	alice.fail("PUT", "/v1/guilds/"+gid+"/owner", 422, "validation", map[string]any{"userId": dave.id}) // dave saiu
	alice.do("PUT", "/v1/guilds/"+gid+"/owner", 204, map[string]any{"userId": bob.id})
	alice.fail("DELETE", "/v1/guilds/"+gid, 403, "forbidden", nil)
	bob.do("DELETE", "/v1/guilds/"+gid, 204, nil)
	alice.fail("GET", "/v1/guilds/"+gid, 404, "not_found", nil)
}

func TestInviteExpiresByTime(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, map[string]any{"expiresIn": 1})
	alice.do("POST", "/v1/guilds/"+gid+"/invites", 422, map[string]any{"maxUses": 0})
	time.Sleep(1100 * time.Millisecond)
	bob.fail("POST", "/v1/invites/"+code+"/join", 410, "invite_expired", nil)
}

// Dois entrando com o último uso: só um passa.
func TestConcurrentJoinRespectsMaxUses(t *testing.T) {
	srv := newTestServer(t).URL
	alice := newUser(t, srv, "alice")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, map[string]any{"maxUses": 1})
	var users []*user
	for i := range 6 {
		users = append(users, newUser(t, srv, fmt.Sprintf("joiner%d", i)))
	}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := call(t, "POST", srv+"/v1/invites/"+code+"/join", u.token, nil); status == 200 {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("%d joins succeeded, queria 1", ok.Load())
	}
}

func TestRolesHierarchyAndGrantLimits(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	base := "/v1/guilds/" + gid

	// bob gerencia papéis, mas só concede o que tem
	mgr := alice.do("POST", base+"/roles", 201, map[string]any{"name": "mgr", "permissions": perm(permissions.ManageRoles, permissions.KickMembers)})["id"].(string)
	alice.do("PUT", base+"/members/"+bob.id+"/roles/"+mgr, 204, nil)
	bob.fail("POST", base+"/roles", 403, "missing_permission", map[string]any{"name": "x", "permissions": perm(permissions.BanMembers)})
	bob.fail("POST", base+"/roles", 403, "missing_permission", map[string]any{"name": "x", "permissions": perm(permissions.Administrator)})
	bob.fail("POST", base+"/roles", 422, "validation", map[string]any{"name": "x", "permissions": 1 << 40})
	low := bob.do("POST", base+"/roles", 201, map[string]any{"name": "low", "permissions": perm(permissions.KickMembers)})["id"].(string)

	// "low" nasce abaixo de mgr: bob a gerencia; mgr (acima dele) ele não
	bob.do("PATCH", base+"/roles/"+low, 200, map[string]any{"name": "low2"})
	bob.fail("PATCH", base+"/roles/"+mgr, 403, "hierarchy", map[string]any{"name": "hack"})
	bob.fail("DELETE", base+"/roles/"+mgr, 403, "hierarchy", nil)
	bob.fail("PUT", base+"/members/"+carol.id+"/roles/"+mgr, 403, "hierarchy", nil)
	bob.do("PUT", base+"/members/"+carol.id+"/roles/"+low, 204, nil)
	// não dá para subir "low" para cima do próprio mgr
	bob.fail("PATCH", base+"/roles/"+low, 403, "hierarchy", map[string]any{"position": 5})
	// nem mexer em quem está acima (alice, dona)
	bob.fail("PUT", base+"/members/"+alice.id+"/roles/"+low, 403, "hierarchy", nil)
	// @everyone: não renomeia, não apaga, não atribui
	bob.fail("PATCH", base+"/roles/"+gid, 422, "validation", map[string]any{"name": "x"})
	bob.fail("DELETE", base+"/roles/"+gid, 422, "validation", nil)
	bob.fail("PUT", base+"/members/"+carol.id+"/roles/"+gid, 422, "validation", nil)

	// o dono reordena: depois de mover "low" para o topo a ordem muda; @everyone fica em 0
	alice.do("PATCH", base+"/roles/"+low, 200, map[string]any{"position": 2})
	var order []string
	for _, r := range items(alice.do("GET", base+"/roles", 200, nil)) {
		order = append(order, r["name"].(string))
	}
	if !eq(order, []string{"@everyone", "mgr", "low2"}) {
		t.Fatalf("order = %v", order)
	}
	// papel apagado some dos membros
	alice.do("DELETE", base+"/roles/"+low, 204, nil)
	for _, m := range items(alice.do("GET", base+"/members", 200, nil)) {
		if m["user"].(map[string]any)["id"] == carol.id && len(m["roleIds"].([]any)) != 0 {
			t.Fatalf("carol keeps deleted role: %v", m)
		}
	}
}
