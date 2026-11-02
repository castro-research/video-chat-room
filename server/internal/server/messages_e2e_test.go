package server

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"kurz/server/internal/permissions"
)

func idsOf(ms []map[string]any) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m["id"].(string))
	}
	return out
}

// send manda n mensagens "m1".."mN" e devolve os ids em ordem.
func sendN(u *user, ch string, n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, u.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": fmt.Sprintf("m%d", i)})["id"].(string))
	}
	return out
}

func TestMessagesSendListCursors(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	base := "/v1/channels/" + ch + "/messages"

	// vazio
	empty := alice.do("GET", base, 200, nil)
	if len(items(empty)) != 0 || empty["hasMore"] != false || empty["hasNewer"] != false {
		t.Fatalf("empty = %v", empty)
	}

	sent := sendN(alice, ch, 10)
	// envio devolve autor, texto, nonce, reactions/attachments vazios
	m := bob.do("POST", base, 201, map[string]any{"text": "oi", "nonce": "n-1"})
	if m["nonce"] != "n-1" || m["text"] != "oi" || m["channelId"] != ch || m["author"].(map[string]any)["id"] != bob.id ||
		len(m["reactions"].([]any)) != 0 || len(m["attachments"].([]any)) != 0 {
		t.Fatalf("message = %v", m)
	}
	// o nonce não é persistido
	for _, x := range items(alice.do("GET", base, 200, nil)) {
		if _, ok := x["nonce"]; ok {
			t.Fatalf("nonce persisted: %v", x)
		}
	}
	sent = append(sent, m["id"].(string)) // 11 mensagens

	list := func(q string) (ids []string, more, newer bool) {
		out := alice.do("GET", base+"?"+q, 200, nil)
		for _, x := range items(out) {
			ids = append(ids, x["id"].(string))
		}
		return ids, out["hasMore"].(bool), out["hasNewer"].(bool)
	}
	// mais recentes: ordem crescente
	got, more, newer := list("limit=4")
	if !eq(got, sent[7:]) || !more || newer {
		t.Fatalf("latest = %v %v %v", got, more, newer)
	}
	// before: a página anterior
	got, more, newer = list("limit=4&before=" + sent[7])
	if !eq(got, sent[3:7]) || !more || !newer {
		t.Fatalf("before = %v %v %v", got, more, newer)
	}
	got, more, _ = list("limit=4&before=" + sent[3])
	if !eq(got, sent[0:3]) || more {
		t.Fatalf("before oldest = %v %v", got, more)
	}
	// after: caminhando para frente
	got, more, newer = list("limit=4&after=" + sent[2])
	if !eq(got, sent[3:7]) || !more || !newer {
		t.Fatalf("after = %v %v %v", got, more, newer)
	}
	got, _, newer = list("limit=100&after=" + sent[9])
	if !eq(got, sent[10:]) || newer {
		t.Fatalf("after tail = %v %v", got, newer)
	}
	got, _, newer = list("after=" + sent[10])
	if len(got) != 0 || newer {
		t.Fatalf("after last = %v", got)
	}
	// around: mensagem do meio com vizinhos dos dois lados
	got, more, newer = list("limit=6&around=" + sent[5])
	if !eq(got, sent[2:8]) || !more || !newer {
		t.Fatalf("around = %v (queria %v) %v %v", got, sent[2:8], more, newer)
	}
	// a paginação completa por before reconstrói tudo sem repetir nem pular
	var all []string
	cursor := ""
	for {
		q := "limit=3"
		if cursor != "" {
			q += "&before=" + cursor
		}
		page, more, _ := list(q)
		all = append(page, all...)
		if !more {
			break
		}
		cursor = page[0]
	}
	if !eq(all, sent) {
		t.Fatalf("walk = %v", all)
	}

	// validações da query
	alice.fail("GET", base+"?limit=0", 422, "validation", nil)
	alice.fail("GET", base+"?limit=101", 422, "validation", nil)
	alice.fail("GET", base+"?before=abc", 422, "validation", nil)
	alice.fail("GET", base+"?before=1&after=2", 422, "validation", nil)

	// ids de mensagens são estritamente crescentes
	for i := 1; i < len(sent); i++ {
		if len(sent[i]) < len(sent[i-1]) || (len(sent[i]) == len(sent[i-1]) && sent[i] <= sent[i-1]) {
			t.Fatalf("ids not increasing: %v", sent)
		}
	}
}

func TestMessagesEditDeleteReply(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	other := alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "outro", "kind": "text"})["id"].(string)
	base := "/v1/channels/" + ch + "/messages"

	a1 := alice.do("POST", base, 201, map[string]any{"text": "original"})["id"].(string)
	b1 := bob.do("POST", base, 201, map[string]any{"text": "do bob"})["id"].(string)
	foreign := alice.do("POST", "/v1/channels/"+other+"/messages", 201, map[string]any{"text": "em outro canal"})["id"].(string)

	// validações de texto
	alice.fail("POST", base, 422, "validation", map[string]any{"text": ""})
	alice.fail("POST", base, 422, "validation", map[string]any{"text": "   \n"})
	alice.fail("POST", base, 422, "validation", map[string]any{"text": strings.Repeat("a", 4001)})
	alice.do("POST", base, 201, map[string]any{"text": strings.Repeat("ã", 4000)}) // 4000 caracteres, mais de 4000 bytes
	alice.fail("POST", base, 422, "validation", map[string]any{"text": "x", "nonce": strings.Repeat("n", 65)})

	// reply
	r := bob.do("POST", base, 201, map[string]any{"text": "resposta", "replyToId": a1})
	if r["replyToId"] != a1 {
		t.Fatalf("reply = %v", r)
	}
	bob.fail("POST", base, 422, "validation", map[string]any{"text": "x", "replyToId": foreign})  // outro canal
	bob.fail("POST", base, 422, "validation", map[string]any{"text": "x", "replyToId": "999999"}) // inexistente
	bob.fail("POST", base, 422, "validation", map[string]any{"text": "x", "replyToId": "abc"})

	// editar: só a própria
	e := alice.do("PATCH", base+"/"+a1, 200, map[string]any{"text": "editado"})
	if e["text"] != "editado" || e["editedAt"] == nil {
		t.Fatalf("edit = %v", e)
	}
	bob.fail("PATCH", base+"/"+a1, 403, "forbidden", map[string]any{"text": "hack"})
	alice.fail("PATCH", base+"/"+b1, 403, "forbidden", map[string]any{"text": "hack"}) // nem o dono edita a alheia
	alice.fail("PATCH", base+"/"+a1, 422, "validation", map[string]any{"text": ""})
	alice.fail("PATCH", base+"/"+foreign, 404, "not_found", map[string]any{"text": "x"}) // mensagem de outro canal
	if m := items(alice.do("GET", base+"?limit=100", 200, nil)); m[0]["editedAt"] == nil || m[len(m)-1]["editedAt"] != nil {
		t.Fatalf("editedAt only on the edited one: %v", m)
	}

	// apagar: própria sim, alheia só com ManageMessages
	bob.fail("DELETE", base+"/"+a1, 403, "missing_permission", nil)
	carol.do("POST", base, 201, map[string]any{"text": "da carol"})
	mod := alice.do("POST", "/v1/guilds/"+gid+"/roles", 201, map[string]any{"name": "mod", "permissions": perm(permissions.ManageMessages)})["id"].(string)
	alice.do("PUT", "/v1/guilds/"+gid+"/members/"+carol.id+"/roles/"+mod, 204, nil)
	carol.do("DELETE", base+"/"+a1, 204, nil) // moderadora apaga a da alice
	carol.fail("DELETE", base+"/"+a1, 404, "not_found", nil)
	bob.do("DELETE", base+"/"+b1, 204, nil) // a própria
	// a resposta sobrevive à mensagem apagada, sem apontar para o vazio
	for _, m := range items(alice.do("GET", base+"?limit=100", 200, nil)) {
		if m["id"] == r["id"] {
			if _, ok := m["replyToId"]; ok {
				t.Fatalf("reply to deleted keeps pointer: %v", m)
			}
		}
		if m["id"] == a1 || m["id"] == b1 {
			t.Fatalf("deleted message listed: %v", m)
		}
	}
}

func TestMessagesReactions(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	bob.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	carol.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	base := "/v1/channels/" + ch + "/messages"
	mid := alice.do("POST", base, 201, map[string]any{"text": "reaja"})["id"].(string)
	react := func(emoji string) string { return base + "/" + mid + "/reactions/" + url.PathEscape(emoji) }

	alice.do("PUT", react("👍")+"/@me", 204, nil)
	alice.do("PUT", react("👍")+"/@me", 204, nil) // idempotente
	bob.do("PUT", react("👍")+"/@me", 204, nil)
	bob.do("PUT", react("🎉")+"/@me", 204, nil)

	view := func(u *user) []map[string]any {
		ms := items(u.do("GET", base, 200, nil))
		var out []map[string]any
		for _, r := range ms[0]["reactions"].([]any) {
			out = append(out, r.(map[string]any))
		}
		return out
	}
	if rs := view(alice); len(rs) != 2 || rs[0]["emoji"] != "👍" || rs[0]["count"] != 2.0 || rs[0]["me"] != true ||
		rs[1]["emoji"] != "🎉" || rs[1]["count"] != 1.0 || rs[1]["me"] != false {
		t.Fatalf("alice sees %v", rs)
	}
	if rs := view(bob); rs[1]["me"] != true {
		t.Fatalf("bob sees %v", rs)
	}

	// remover: a própria; a alheia só com ManageMessages
	bob.do("DELETE", react("🎉")+"/@me", 204, nil)
	bob.do("DELETE", react("🎉")+"/@me", 204, nil) // idempotente
	if rs := view(alice); len(rs) != 1 {
		t.Fatalf("after remove: %v", rs)
	}
	carol.fail("DELETE", react("👍")+"/"+bob.id, 403, "missing_permission", nil)
	alice.do("DELETE", react("👍")+"/"+bob.id, 204, nil) // dono tem ManageMessages
	if rs := view(alice); rs[0]["count"] != 1.0 {
		t.Fatalf("after mod remove: %v", rs)
	}

	// validações
	alice.fail("PUT", react("a b")+"/@me", 422, "validation", nil)
	alice.fail("PUT", react(strings.Repeat("x", 65))+"/@me", 422, "validation", nil)
	alice.fail("PUT", base+"/999999/reactions/"+url.PathEscape("👍")+"/@me", 404, "not_found", nil)
	for i := range 19 { // 👍 + 19 = 20 emojis distintos
		alice.do("PUT", react(fmt.Sprintf("e%d", i))+"/@me", 204, nil)
	}
	alice.fail("PUT", react("extra")+"/@me", 422, "validation", nil)
	alice.do("PUT", react("e0")+"/@me", 204, nil) // já existente continua valendo

	// apagar a mensagem leva as reações
	alice.do("DELETE", base+"/"+mid, 204, nil)
	alice.fail("PUT", react("👍")+"/@me", 404, "not_found", nil)
}

func TestMessagesPermissionMatrix(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob, carol, dan := newUser(t, srv, "alice"), newUser(t, srv, "bob"), newUser(t, srv, "carol"), newUser(t, srv, "dan")
	gid := createGuild(alice, "Casa")
	code := invite(alice, gid, nil)
	for _, u := range []*user{bob, carol} {
		u.do("POST", "/v1/invites/"+code+"/join", 200, nil)
	}
	gbase := "/v1/guilds/" + gid
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	base := "/v1/channels/" + ch + "/messages"
	mid := alice.do("POST", base, 201, map[string]any{"text": "oi"})["id"].(string)

	// não-membro: 404 em tudo
	dan.fail("GET", base, 404, "not_found", nil)
	dan.fail("POST", base, 404, "not_found", map[string]any{"text": "x"})
	dan.fail("PATCH", base+"/"+mid, 404, "not_found", map[string]any{"text": "x"})
	dan.fail("DELETE", base+"/"+mid, 404, "not_found", nil)
	dan.fail("PUT", base+"/"+mid+"/reactions/x/@me", 404, "not_found", nil)
	dan.fail("POST", "/v1/channels/"+ch+"/read", 404, "not_found", map[string]any{"messageId": mid})
	dan.fail("GET", gbase+"/read", 404, "not_found", nil)
	// sem token
	if status, _ := call(t, "GET", srv+base, "", nil); status != 401 {
		t.Fatalf("no token = %d", status)
	}
	// canal inexistente / id malformado
	bob.fail("GET", "/v1/channels/999999/messages", 404, "not_found", nil)
	bob.fail("GET", "/v1/channels/abc/messages", 404, "not_found", nil)

	// sem SendMessages: lê mas não escreve nem reage; ainda remove a própria reação (nenhuma) e marca como lido
	alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.SendMessages)})
	bob.do("GET", base, 200, nil)
	bob.fail("POST", base, 403, "missing_permission", map[string]any{"text": "x"})
	bob.fail("PUT", base+"/"+mid+"/reactions/x/@me", 403, "missing_permission", nil)
	bob.do("POST", "/v1/channels/"+ch+"/read", 204, map[string]any{"messageId": mid})

	// canal oculto por override: some até para ler (404), mesmo sendo membro
	alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+carol.id, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
	carol.fail("GET", base, 404, "not_found", nil)
	carol.fail("POST", base, 404, "not_found", map[string]any{"text": "x"})
	carol.fail("DELETE", base+"/"+mid, 404, "not_found", nil)
	carol.fail("POST", "/v1/channels/"+ch+"/read", 404, "not_found", map[string]any{"messageId": mid})
	if rs := items(carol.do("GET", gbase+"/read", 200, nil)); len(rs) != 0 {
		t.Fatalf("hidden channel in read states: %v", rs)
	}

	// só ManageMessages sem SendMessages ainda apaga a alheia
	alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.SendMessages), "allow": perm(permissions.ManageMessages)})
	bob.do("DELETE", base+"/"+mid, 204, nil)

	// canal de voz e categoria não têm mensagens
	voice := alice.do("POST", gbase+"/channels", 201, map[string]any{"name": "Sala", "kind": "voice"})["id"].(string)
	alice.fail("POST", "/v1/channels/"+voice+"/messages", 422, "validation", map[string]any{"text": "x"})
	alice.fail("GET", "/v1/channels/"+voice+"/messages", 422, "validation", nil)
}

func TestMessagesReadState(t *testing.T) {
	srv := newTestServer(t).URL
	alice, bob := newUser(t, srv, "alice"), newUser(t, srv, "bob")
	gid := createGuild(alice, "Casa")
	bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	ch2 := alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "dois", "kind": "text"})["id"].(string)
	alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "Sala", "kind": "voice"})
	rbase := "/v1/channels/" + ch + "/read"

	ms := sendN(alice, ch, 5)
	alice.do("POST", "/v1/channels/"+ch2+"/messages", 201, map[string]any{"text": "x"})

	rs := bob.do("GET", rbase, 200, nil)
	if rs["unread"] != 5.0 || rs["channelId"] != ch {
		t.Fatalf("bob unread = %v", rs)
	}
	if _, ok := rs["lastReadMessageId"]; ok {
		t.Fatalf("never read but has lastReadMessageId: %v", rs)
	}
	// as próprias mensagens não contam
	if rs := alice.do("GET", rbase, 200, nil); rs["unread"] != 0.0 {
		t.Fatalf("alice unread = %v", rs)
	}

	bob.do("POST", rbase, 204, map[string]any{"messageId": ms[2]})
	rs = bob.do("GET", rbase, 200, nil)
	if rs["unread"] != 2.0 || rs["lastReadMessageId"] != ms[2] {
		t.Fatalf("after ack = %v", rs)
	}
	// não recua
	bob.do("POST", rbase, 204, map[string]any{"messageId": ms[0]})
	if rs := bob.do("GET", rbase, 200, nil); rs["lastReadMessageId"] != ms[2] {
		t.Fatalf("ack went backwards: %v", rs)
	}
	// alias /ack
	bob.do("POST", "/v1/channels/"+ch+"/ack", 204, map[string]any{"messageId": ms[4]})
	if rs := bob.do("GET", rbase, 200, nil); rs["unread"] != 0.0 {
		t.Fatalf("after ack alias = %v", rs)
	}
	bob.fail("POST", rbase, 422, "validation", map[string]any{"messageId": "999999"})
	bob.fail("POST", rbase, 422, "validation", map[string]any{"messageId": ""})
	other := alice.do("GET", "/v1/channels/"+ch2+"/messages", 200, nil)
	bob.fail("POST", rbase, 422, "validation", map[string]any{"messageId": idsOf(items(other))[0]}) // de outro canal

	// por guild: só canais de texto visíveis
	byCh := map[string]map[string]any{}
	for _, x := range items(bob.do("GET", "/v1/guilds/"+gid+"/read", 200, nil)) {
		byCh[x["channelId"].(string)] = x
	}
	if len(byCh) != 2 || byCh[ch]["unread"] != 0.0 || byCh[ch2]["unread"] != 1.0 {
		t.Fatalf("guild read states = %v", byCh)
	}
}

// Envios concorrentes ao mesmo canal: nenhum se perde e a ordem por id é a ordem de commit.
func TestMessagesConcurrentSendsAreMonotonic(t *testing.T) {
	srv := newTestServer(t).URL
	alice := newUser(t, srv, "alice")
	gid := createGuild(alice, "Casa")
	ch := channelNamed(alice, gid, "geral")["id"].(string)
	base := "/v1/channels/" + ch + "/messages"
	var users []*user
	for i := range 5 {
		u := newUser(t, srv, fmt.Sprintf("sender%d", i))
		u.do("POST", "/v1/invites/"+invite(alice, gid, map[string]any{"maxUses": 1})+"/join", 200, nil)
		users = append(users, u)
	}
	const perUser = 20
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perUser {
				if status, out := call(t, "POST", srv+base, u.token, map[string]any{"text": fmt.Sprintf("%s-%d", u.id, i)}); status != 201 {
					t.Errorf("send = %d %v", status, out)
				}
			}
		}()
	}
	wg.Wait()

	var all []map[string]any
	cursor := ""
	for {
		q := "?limit=100"
		if cursor != "" {
			q += "&after=" + cursor
		}
		page := alice.do("GET", base+q, 200, nil)
		its := items(page)
		all = append(all, its...)
		if !page["hasNewer"].(bool) {
			break
		}
		cursor = its[len(its)-1]["id"].(string)
	}
	if len(all) != len(users)*perUser {
		t.Fatalf("got %d messages, queria %d", len(all), len(users)*perUser)
	}
	seen := map[string]bool{}
	for i, m := range all {
		if seen[m["id"].(string)] {
			t.Fatalf("duplicate %v", m["id"])
		}
		seen[m["id"].(string)] = true
		if i > 0 {
			prev, cur := all[i-1]["id"].(string), m["id"].(string)
			if len(cur) < len(prev) || (len(cur) == len(prev) && cur <= prev) {
				t.Fatalf("ids out of order at %d: %s then %s", i, prev, cur)
			}
		}
	}
	// ordem por usuário preservada (cada um enviou em sequência)
	last := map[string]int{}
	for _, m := range all {
		var uid string
		var n int
		if _, err := fmt.Sscanf(m["text"].(string), "%s", &uid); err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(uid, "-", 2)
		fmt.Sscanf(parts[1], "%d", &n)
		if prev, ok := last[parts[0]]; ok && n != prev+1 {
			t.Fatalf("user %s: %d after %d", parts[0], n, prev)
		}
		last[parts[0]] = n
	}
}
