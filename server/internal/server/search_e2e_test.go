package server

import (
	"net/url"
	"strings"
	"testing"

	"kurz/server/internal/permissions"
)

func searchIDs(u *user, gid string, query url.Values) (ids []string, next string) {
	u.t.Helper()
	out := u.do("GET", "/v1/guilds/"+gid+"/search?"+query.Encode(), 200, nil)
	for _, it := range items(out) {
		ids = append(ids, it["id"].(string))
	}
	next, _ = out["nextCursor"].(string)
	return
}

func TestSearchRespectsPermissionsFiltersAndPagination(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	geral := channelNamed(alice, gid, "geral")["id"].(string)
	secreto := alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "secreto", "kind": "text"})["id"].(string)
	alice.do("PUT", "/v1/channels/"+secreto+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.ViewChannel)})

	send := func(u *user, ch, text string) string {
		return u.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": text})["id"].(string)
	}
	a1 := send(alice, geral, "o bolo de chocolate está pronto")
	b1 := send(bob, geral, "adorei o bolo, quero outro bolo")
	a2 := send(alice, geral, "reunião amanhã cedo")
	hidden := send(alice, secreto, "o bolo secreto fica aqui")
	a3 := send(alice, geral, "mais um bolo para a festa")
	send(bob, geral, "nada a ver")

	q := func(s string, extra ...string) url.Values {
		v := url.Values{"q": {s}}
		for i := 0; i+1 < len(extra); i += 2 {
			v.Set(extra[i], extra[i+1])
		}
		return v
	}

	// bob não vê o canal secreto: o resultado de lá nunca aparece. Mais novas primeiro.
	got, _ := searchIDs(bob, gid, q("bolo"))
	if !eq(got, []string{a3, b1, a1}) {
		t.Fatalf("bob search = %v, queria %v", got, []string{a3, b1, a1})
	}
	// alice (dona) vê tudo
	got, _ = searchIDs(alice, gid, q("bolo"))
	if !eq(got, []string{a3, hidden, b1, a1}) {
		t.Fatalf("alice search = %v", got)
	}
	// vira visível quando o override sai (a permissão é resolvida a cada consulta)
	alice.do("DELETE", "/v1/channels/"+secreto+"/overrides/member/"+bob.id, 204, nil)
	if got, _ = searchIDs(bob, gid, q("bolo")); len(got) != 4 {
		t.Fatalf("after override removal = %v", got)
	}
	alice.do("PUT", "/v1/channels/"+secreto+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.ViewChannel)})

	// pedir o canal oculto explicitamente é 404, como ler as mensagens dele
	bob.fail("GET", "/v1/guilds/"+gid+"/search?"+q("bolo", "channelId", secreto).Encode(), 404, "not_found", nil)
	// filtro de canal e de autor
	if got, _ = searchIDs(alice, gid, q("bolo", "channelId", secreto)); !eq(got, []string{hidden}) {
		t.Fatalf("channel filter = %v", got)
	}
	if got, _ = searchIDs(bob, gid, q("bolo", "authorId", bob.id)); !eq(got, []string{b1}) {
		t.Fatalf("author filter = %v", got)
	}
	if got, _ = searchIDs(bob, gid, q("bolo", "authorId", alice.id, "channelId", geral)); !eq(got, []string{a3, a1}) {
		t.Fatalf("author+channel filter = %v", got)
	}

	// paginação por cursor: sem repetir nem pular
	page1, next := searchIDs(alice, gid, q("bolo", "limit", "3"))
	if !eq(page1, []string{a3, hidden, b1}) || next != b1 {
		t.Fatalf("page1 = %v next=%q", page1, next)
	}
	page2, next2 := searchIDs(alice, gid, q("bolo", "limit", "3", "cursor", next))
	if !eq(page2, []string{a1}) || next2 != "" {
		t.Fatalf("page2 = %v next=%q", page2, next2)
	}
	// exatamente no limite: sem nextCursor
	if _, n := searchIDs(alice, gid, q("bolo", "limit", "4")); n != "" {
		t.Fatalf("full page must not have nextCursor, got %q", n)
	}

	// sintaxe websearch: frase, negação, OU; stemming PT (casa/casas)
	if got, _ = searchIDs(alice, gid, q(`"bolo de chocolate"`)); !eq(got, []string{a1}) {
		t.Fatalf("phrase = %v", got)
	}
	if got, _ = searchIDs(alice, gid, q("bolo -chocolate")); !eq(got, []string{a3, hidden, b1}) {
		t.Fatalf("negation = %v", got)
	}
	if got, _ = searchIDs(alice, gid, q("chocolate or reunião")); !eq(got, []string{a2, a1}) {
		t.Fatalf("or = %v", got)
	}
	if got, _ = searchIDs(alice, gid, q("zzzinexistente")); len(got) != 0 {
		t.Fatalf("no hits = %v", got)
	}

	// destaque opcional
	out := alice.do("GET", "/v1/guilds/"+gid+"/search?"+q("chocolate", "highlight", "true").Encode(), 200, nil)
	hl := items(out)[0]["highlight"].(string)
	if hl != "bolo de ⟦chocolate⟧ está pronto" { // ts_headline descarta a stopword inicial
		t.Fatalf("highlight = %q", hl)
	}
	if _, ok := items(alice.do("GET", "/v1/guilds/"+gid+"/search?"+q("chocolate").Encode(), 200, nil))[0]["highlight"]; ok {
		t.Fatal("no highlight unless asked")
	}
	// resultado traz autor e canal
	first := items(out)[0]
	if first["channelId"] != geral || first["author"].(map[string]any)["username"] != "alice" || first["text"] == "" {
		t.Fatalf("result = %v", first)
	}

	// erros: q vazio/longo, limit/cursor inválidos, não-membro (404), guild inexistente
	alice.fail("GET", "/v1/guilds/"+gid+"/search", 422, "validation", nil)
	alice.fail("GET", "/v1/guilds/"+gid+"/search?q=%20%20", 422, "validation", nil)
	alice.fail("GET", "/v1/guilds/"+gid+"/search?q="+strings.Repeat("a", 201), 422, "validation", nil)
	alice.fail("GET", "/v1/guilds/"+gid+"/search?q=bolo&limit=0", 422, "validation", nil)
	alice.fail("GET", "/v1/guilds/"+gid+"/search?q=bolo&cursor=xx", 422, "validation", nil)
	carol.fail("GET", "/v1/guilds/"+gid+"/search?q=bolo", 404, "not_found", nil)
	alice.fail("GET", "/v1/guilds/999/search?q=bolo", 404, "not_found", nil)
	if status, _ := call(t, "GET", srv+"/v1/guilds/"+gid+"/search?q=bolo", "", nil); status != 401 {
		t.Fatalf("no token = %d", status)
	}

	// outra guild não vaza: o mesmo termo, mesmo usuário
	other := createGuild(carol, "Outra")
	send(carol, channelNamed(carol, other, "geral")["id"].(string), "bolo de outra guild")
	if got, _ = searchIDs(alice, gid, q("bolo")); len(got) != 4 {
		t.Fatalf("cross-guild leak: %v", got)
	}
	// mensagem apagada sai da busca; editada é reindexada
	alice.do("DELETE", "/v1/channels/"+geral+"/messages/"+a1, 204, nil)
	alice.do("PATCH", "/v1/channels/"+geral+"/messages/"+a3, 200, map[string]any{"text": "agora é torta"})
	if got, _ = searchIDs(alice, gid, q("bolo")); !eq(got, []string{hidden, b1}) {
		t.Fatalf("after delete/edit = %v", got)
	}
	if got, _ = searchIDs(alice, gid, q("torta")); !eq(got, []string{a3}) {
		t.Fatalf("edited reindexed = %v", got)
	}
}
