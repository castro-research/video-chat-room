package server

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kurz/server/internal/jobs"
	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/objstore"
)

type storageEnv struct {
	url   string
	app   *App
	pool  *pgxpool.Pool
	store objstore.Store
}

// storageBackends roda o teste contra o fake em memória e, se KURZ_TEST_S3_ENDPOINT estiver setada, contra um S3 real (SeaweedFS do compose).
// Sem a variável o subteste "s3" aparece como SKIP explícito.
func storageBackends(t *testing.T, fn func(t *testing.T, e storageEnv)) {
	t.Run("fake", func(t *testing.T) {
		f := objstore.NewFake()
		fs := httptest.NewServer(f)
		t.Cleanup(fs.Close)
		f.SetBaseURL(fs.URL)
		fn(t, newStorageEnv(t, f))
	})
	t.Run("s3", func(t *testing.T) {
		ep := os.Getenv("KURZ_TEST_S3_ENDPOINT")
		if ep == "" {
			t.Skip("KURZ_TEST_S3_ENDPOINT not set: S3 real não testado (make s3-up)")
		}
		b := make([]byte, 5)
		_, _ = crand.Read(b)
		s3, err := objstore.NewS3(context.Background(), objstore.S3Config{Endpoint: ep, Bucket: "kurz-test-" + hex.EncodeToString(b), AccessKey: "kurzdev", SecretKey: "kurzdev-secret"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s3.RemoveBucket(context.Background()) })
		fn(t, newStorageEnv(t, s3))
	})
}

func newStorageEnv(t *testing.T, store objstore.Store) storageEnv {
	t.Helper()
	cfg := config.Config{ServerName: "Teste", Registration: "open", JWTSecret: []byte(strings.Repeat("k", 32))}
	pool := dbtest.New(t)
	app := NewApp(cfg, pool, WithObjectStore(store), WithJobOptions(jobs.Options{Backoff: func(int) time.Duration { return 0 }}))
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	return storageEnv{url: srv.URL, app: app, pool: pool, store: store}
}

func putObject(t *testing.T, url string, data []byte, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("PUT = %d, queria %d: %s", res.StatusCode, want, b)
	}
}

func getObject(t *testing.T, url string) (int, []byte, http.Header) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

func requestUpload(u *user, ch, name, ct string, size int) map[string]any {
	u.t.Helper()
	return u.do("POST", "/v1/channels/"+ch+"/attachments", 201, map[string]any{"filename": name, "contentType": ct, "size": size})
}

func objectKey(t *testing.T, e storageEnv, id string) string {
	t.Helper()
	var key string
	if err := e.pool.QueryRow(context.Background(), `SELECT object_key FROM attachments WHERE id = $1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestAttachmentFlowEndToEnd(t *testing.T) {
	storageBackends(t, func(t *testing.T, e storageEnv) {
		alice, bob := newUser(t, e.url, "alice"), newUser(t, e.url, "bob")
		gid := createGuild(alice, "Casa")
		bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
		ch := channelNamed(alice, gid, "geral")["id"].(string)
		data := []byte("\x89PNG not really, but bytes are bytes")

		// 1) pede o upload
		up := requestUpload(alice, ch, "foto.png", "image/png", len(data))
		if up["method"] != "PUT" || up["uploadUrl"] == "" || up["id"] == "" {
			t.Fatalf("upload ticket = %v", up)
		}
		if exp, _ := time.Parse(time.RFC3339, up["expiresAt"].(string)); time.Until(exp) <= 0 || time.Until(exp) > 20*time.Minute {
			t.Fatalf("expiresAt = %v", up["expiresAt"])
		}
		id := up["id"].(string)

		// ainda não enviou: a mensagem é recusada
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"attachmentIds": []string{id}})

		// 2) PUT direto no storage
		putObject(t, up["uploadUrl"].(string), data, 200)

		// 3) mensagem só com anexo (texto vazio)
		m := alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "", "attachmentIds": []string{id}, "nonce": "n1"})
		atts := m["attachments"].([]any)
		if m["text"] != "" || len(atts) != 1 {
			t.Fatalf("message = %v", m)
		}
		a := atts[0].(map[string]any)
		if a["id"] != id || a["name"] != "foto.png" || a["contentType"] != "image/png" || int(a["size"].(float64)) != len(data) || a["url"] == "" {
			t.Fatalf("attachment = %v", a)
		}

		// 4) o GET pré-assinado entrega os bytes
		code, body, hdr := getObject(t, a["url"].(string))
		if code != 200 || !bytes.Equal(body, data) || !strings.HasPrefix(hdr.Get("Content-Type"), "image/png") {
			t.Fatalf("GET = %d %q %v", code, body, hdr)
		}

		// listagem (outro usuário) também traz o anexo com URL válida
		list := items(bob.do("GET", "/v1/channels/"+ch+"/messages", 200, nil))
		la := list[0]["attachments"].([]any)
		if len(la) != 1 {
			t.Fatalf("list = %v", list)
		}
		if code, body, _ := getObject(t, la[0].(map[string]any)["url"].(string)); code != 200 || !bytes.Equal(body, data) {
			t.Fatalf("list GET = %d", code)
		}

		// o evento do outbox (que vira message.create no gateway) leva o anexo
		var payload string
		if err := e.pool.QueryRow(context.Background(), `SELECT payload::text FROM outbox WHERE type = 'message.create' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload, `"foto.png"`) || !strings.Contains(payload, `"attachments": [{`) {
			t.Fatalf("outbox payload without attachment: %s", payload)
		}

		// o anexo já está ligado: não dá para usar de novo
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "de novo", "attachmentIds": []string{id}})

		// texto + vários anexos
		u1, u2 := requestUpload(alice, ch, "a.pdf", "application/pdf", 3), requestUpload(alice, ch, "b.txt", "text/plain; charset=utf-8", 2)
		putObject(t, u1["uploadUrl"].(string), []byte("abc"), 200)
		putObject(t, u2["uploadUrl"].(string), []byte("hi"), 200)
		m2 := alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "docs", "attachmentIds": []string{u1["id"].(string), u2["id"].(string)}})
		if len(m2["attachments"].([]any)) != 2 || m2["text"] != "docs" {
			t.Fatalf("m2 = %v", m2)
		}

		// 5) apagar a mensagem enfileira a remoção do objeto; o worker o apaga
		key := objectKey(t, e, id)
		if _, err := e.store.Stat(context.Background(), key); err != nil {
			t.Fatalf("object must exist before delete: %v", err)
		}
		alice.do("DELETE", "/v1/channels/"+ch+"/messages/"+m["id"].(string), 204, nil)
		if n, err := e.app.RunJobsOnce(context.Background()); err != nil || n != 1 {
			t.Fatalf("jobs run = %d, %v", n, err)
		}
		if _, err := e.store.Stat(context.Background(), key); !errors.Is(err, objstore.ErrNotFound) {
			t.Fatalf("object must be gone after the job: %v", err)
		}
		var rows int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM attachments WHERE id = $1`, id).Scan(&rows)
		if rows != 0 {
			t.Fatal("attachment row must cascade with the message")
		}
	})
}

func TestAttachmentValidationAndPermissions(t *testing.T) {
	storageBackends(t, func(t *testing.T, e storageEnv) {
		alice, bob, carol := newUser(t, e.url, "alice"), newUser(t, e.url, "bob"), newUser(t, e.url, "carol")
		gid := createGuild(alice, "Casa")
		bob.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
		carol.do("POST", "/v1/invites/"+invite(alice, gid, nil)+"/join", 200, nil)
		ch := channelNamed(alice, gid, "geral")["id"].(string)
		other := alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "outro", "kind": "text"})["id"].(string)
		path := "/v1/channels/" + ch + "/attachments"
		body := func(name, ct string, size any) map[string]any {
			return map[string]any{"filename": name, "contentType": ct, "size": size}
		}

		// tipo, tamanho e nome inválidos
		for name, b := range map[string]map[string]any{
			"svg":        body("x.svg", "image/svg+xml", 10),
			"html":       body("x.html", "text/html", 10),
			"exe":        body("x.exe", "application/x-msdownload", 10),
			"garbage ct": body("x", "not a type", 10),
			"too big":    body("x.zip", "application/zip", 25<<20+1),
			"zero":       body("x.zip", "application/zip", 0),
			"empty name": body("  ", "image/png", 10),
			"path name":  body("../etc/passwd", "image/png", 10),
			"slash":      body("a/b.png", "image/png", 10),
			"control":    body("a\x00b.png", "image/png", 10),
			"long name":  body(strings.Repeat("a", 256), "image/png", 10),
		} {
			if out := alice.do("POST", path, 422, b); out["code"] != "validation" {
				t.Errorf("%s: %v", name, out)
			}
		}
		alice.do("POST", path, 201, body("max.zip", "application/zip", 25<<20)) // exatamente o limite passa

		// sem SendMessages: 403; sem ver o canal: 404; canal de voz: 422
		alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+bob.id, 200, map[string]any{"deny": perm(permissions.SendMessages)})
		bob.fail("POST", path, 403, "missing_permission", body("a.png", "image/png", 1))
		alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+carol.id, 200, map[string]any{"deny": perm(permissions.ViewChannel)})
		carol.fail("POST", path, 404, "not_found", body("a.png", "image/png", 1))
		voice := alice.do("POST", "/v1/guilds/"+gid+"/channels", 201, map[string]any{"name": "voz", "kind": "voice"})["id"].(string)
		alice.fail("POST", "/v1/channels/"+voice+"/attachments", 422, "validation", body("a.png", "image/png", 1))
		// sem token
		if status, _ := call(t, "POST", e.url+path, "", body("a.png", "image/png", 1)); status != 401 {
			t.Fatalf("no token = %d", status)
		}

		// anexo de outro canal
		up := requestUpload(alice, ch, "a.png", "image/png", 3)
		putObject(t, up["uploadUrl"].(string), []byte("abc"), 200)
		alice.fail("POST", "/v1/channels/"+other+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{up["id"].(string)}})
		// anexo de outro usuário
		alice.do("PUT", "/v1/channels/"+ch+"/overrides/member/"+bob.id, 200, map[string]any{"allow": perm(permissions.SendMessages)})
		bob.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{up["id"].(string)}})
		// id inexistente / malformado / duplicado / demais
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{"999"}})
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{"abc"}})
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{up["id"].(string), up["id"].(string)}})
		many := make([]string, 11)
		for i := range many {
			many[i] = up["id"].(string)
		}
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": many})
		// texto vazio sem anexo continua inválido
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": ""})

		// tamanho enviado diferente do declarado: o Content-Length vai assinado, então o próprio PUT é recusado
		// e o anexo continua "não enviado"
		bad := requestUpload(alice, ch, "b.png", "image/png", 3)
		putObject(t, bad["uploadUrl"].(string), []byte("abcdef"), 403)
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"text": "x", "attachmentIds": []string{bad["id"].(string)}})

		// nada disso criou mensagem
		if n := len(items(alice.do("GET", "/v1/channels/"+ch+"/messages", 200, nil))); n != 0 {
			t.Fatalf("messages = %d", n)
		}
		// o anexo válido ainda pode ser usado depois das tentativas falhas
		alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "ok", "attachmentIds": []string{up["id"].(string)}})
	})
}

// A URL PUT continua válida por alguns minutos depois do bind: sobrescrever o objeto original não pode alterar o
// anexo servido (que vem de uma cópia final), nem fazer o upload "trocado" passar da validação.
func TestAttachmentOverwriteAfterBindDoesNotChangeServedBytes(t *testing.T) {
	storageBackends(t, func(t *testing.T, e storageEnv) {
		alice := newUser(t, e.url, "alice")
		ch := channelNamed(alice, createGuild(alice, "Casa"), "geral")["id"].(string)
		good := []byte("conteudo validado")
		up := requestUpload(alice, ch, "a.png", "image/png", len(good))
		putObject(t, up["uploadUrl"].(string), good, 200)
		m := alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"attachmentIds": []string{up["id"].(string)}})
		url := m["attachments"].([]any)[0].(map[string]any)["url"].(string)

		// o atacante ainda tem a URL PUT: maior que o declarado é recusado, mas do mesmo tamanho e diferente ele consegue
		putObject(t, up["uploadUrl"].(string), bytes.Repeat([]byte("X"), 4096), 403)
		putObject(t, up["uploadUrl"].(string), bytes.Repeat([]byte("X"), len(good)), 200)

		if code, body, _ := getObject(t, url); code != 200 || !bytes.Equal(body, good) {
			t.Fatalf("served bytes changed after overwrite: %d %q", code, body)
		}
		list := items(alice.do("GET", "/v1/channels/"+ch+"/messages", 200, nil))
		u2 := list[0]["attachments"].([]any)[0].(map[string]any)["url"].(string)
		if code, body, _ := getObject(t, u2); code != 200 || !bytes.Equal(body, good) {
			t.Fatalf("listed bytes changed after overwrite: %d %q", code, body)
		}

		// enviar ANTES do bind com tamanho diferente é recusado no PUT, e o bind recusa o anexo que nunca chegou
		up2 := requestUpload(alice, ch, "b.png", "image/png", 3)
		putObject(t, up2["uploadUrl"].(string), []byte("abcdef"), 403)
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"attachmentIds": []string{up2["id"].(string)}})
	})
}

func TestAttachmentURLsExpire(t *testing.T) {
	f := objstore.NewFake()
	fs := httptest.NewServer(f)
	defer fs.Close()
	f.SetBaseURL(fs.URL)
	e := newStorageEnv(t, f)
	alice := newUser(t, e.url, "alice")
	ch := channelNamed(alice, createGuild(alice, "Casa"), "geral")["id"].(string)
	up := requestUpload(alice, ch, "a.png", "image/png", 1)
	putObject(t, up["uploadUrl"].(string), []byte("x"), 200)
	m := alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"attachmentIds": []string{up["id"].(string)}})
	url := m["attachments"].([]any)[0].(map[string]any)["url"].(string)
	if code, _, _ := getObject(t, url); code != 200 {
		t.Fatalf("fresh url = %d", code)
	}
	f.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if code, _, _ := getObject(t, url); code != 403 {
		t.Fatalf("expired url = %d", code)
	}
	putObject(t, up["uploadUrl"].(string), []byte("x"), 403) // a de upload (5 min) também
}

func TestOrphanCleanupJob(t *testing.T) {
	storageBackends(t, func(t *testing.T, e storageEnv) {
		ctx := context.Background()
		alice := newUser(t, e.url, "alice")
		ch := channelNamed(alice, createGuild(alice, "Casa"), "geral")["id"].(string)

		old := requestUpload(alice, ch, "old.png", "image/png", 1)
		putObject(t, old["uploadUrl"].(string), []byte("x"), 200)
		fresh := requestUpload(alice, ch, "fresh.png", "image/png", 1)
		putObject(t, fresh["uploadUrl"].(string), []byte("y"), 200)
		bound := requestUpload(alice, ch, "bound.png", "image/png", 1)
		putObject(t, bound["uploadUrl"].(string), []byte("z"), 200)
		alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"attachmentIds": []string{bound["id"].(string)}})
		oldKey, freshKey, boundKey := objectKey(t, e, old["id"].(string)), objectKey(t, e, fresh["id"].(string)), objectKey(t, e, bound["id"].(string))

		// envelhece os pendentes e a ligada (que não pode ser tocada, mesmo velha)
		if _, err := e.pool.Exec(ctx, `UPDATE attachments SET created_at = now() - interval '3 hours' WHERE id IN ($1, $2)`, old["id"], bound["id"]); err != nil {
			t.Fatal(err)
		}
		if _, err := e.pool.Exec(ctx, `INSERT INTO jobs (id, kind) VALUES (1, 'attachments.cleanup')`); err != nil {
			t.Fatal(err)
		}
		// roda: a limpeza (1) + a remoção do objeto e da cópia final que ela enfileirou (2)
		if n, err := e.app.RunJobsOnce(ctx); err != nil || n != 3 {
			t.Fatalf("jobs run = %d, %v", n, err)
		}
		if _, err := e.store.Stat(ctx, oldKey); !errors.Is(err, objstore.ErrNotFound) {
			t.Fatalf("orphan object must be deleted: %v", err)
		}
		for _, k := range []string{freshKey, boundKey} {
			if _, err := e.store.Stat(ctx, k); err != nil {
				t.Fatalf("%s must survive: %v", k, err)
			}
		}
		var left int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM attachments`).Scan(&left)
		if left != 2 {
			t.Fatalf("attachment rows = %d, want fresh+bound", left)
		}
		// o upload velho não pode mais virar mensagem
		alice.fail("POST", "/v1/channels/"+ch+"/messages", 422, "validation", map[string]any{"attachmentIds": []string{old["id"].(string)}})
	})
}

func TestAttachmentsNotConfigured(t *testing.T) {
	srv := newTestServer(t).URL
	alice := newUser(t, srv, "alice")
	ch := channelNamed(alice, createGuild(alice, "Casa"), "geral")["id"].(string)
	alice.fail("POST", "/v1/channels/"+ch+"/attachments", 501, "not_supported", map[string]any{"filename": "a.png", "contentType": "image/png", "size": 1})
	alice.fail("POST", "/v1/channels/"+ch+"/messages", 501, "not_supported", map[string]any{"attachmentIds": []string{"1"}})
	// o resto segue funcionando e a capability não é anunciada
	alice.do("POST", "/v1/channels/"+ch+"/messages", 201, map[string]any{"text": "oi"})
	_, info := call(t, "GET", srv+"/.well-known/kurz", "", nil)
	for _, c := range info["capabilities"].([]any) {
		if c == "attachments" {
			t.Fatalf("capabilities = %v", info["capabilities"])
		}
	}
}

func TestDiscoveryAdvertisesAttachmentsWithStorage(t *testing.T) {
	e := newStorageEnv(t, objstore.NewFake())
	_, info := call(t, "GET", e.url+"/.well-known/kurz", "", nil)
	if got := info["capabilities"]; len(got.([]any)) != 4 || got.([]any)[3] != "attachments" {
		t.Fatalf("capabilities = %v", got)
	}
}
