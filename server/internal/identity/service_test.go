package identity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"kurz/server/internal/jobs"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

func newTestService(t *testing.T, registration string) *Service {
	t.Helper()
	return NewService(dbtest.New(t), ids.NewGenerator(1), NewTokenIssuer(testSecret, 15*time.Minute),
		Config{Registration: registration, RefreshTTL: time.Hour})
}

func wantCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != status || he.Code != code {
		t.Fatalf("err = %v, want %d %s", err, status, code)
	}
}

func register(t *testing.T, s *Service, username string) Session {
	t.Helper()
	sess, err := s.Register(context.Background(), RegisterRequest{Username: username, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestRegisterCreatesAccountAndSession(t *testing.T) {
	s := newTestService(t, "open")
	sess, err := s.Register(context.Background(), RegisterRequest{Username: "  Yuri_09 ", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.Username != "yuri_09" || sess.User.Name != "yuri_09" {
		t.Fatalf("user = %+v", sess.User)
	}
	if sess.AccessToken == "" || sess.RefreshToken == "" || sess.ExpiresIn != 900 {
		t.Fatalf("session = %+v", sess)
	}
	if id, err := s.tokens.Parse(sess.AccessToken); err != nil || ids.Format(id) != sess.User.ID {
		t.Fatalf("access token does not belong to the user: id=%d err=%v", id, err)
	}
}

func TestRegisterUsesGivenDisplayName(t *testing.T) {
	s := newTestService(t, "open")
	sess, err := s.Register(context.Background(), RegisterRequest{Username: "yuri", Password: "password123", Name: "Yuri Costa"})
	if err != nil || sess.User.Name != "Yuri Costa" {
		t.Fatalf("sess = %+v, err = %v", sess, err)
	}
}

func TestRegisterRejectsDuplicateUsername(t *testing.T) {
	s := newTestService(t, "open")
	register(t, s, "yuri")
	_, err := s.Register(context.Background(), RegisterRequest{Username: "YURI", Password: "password123"})
	wantCode(t, err, 409, "username_taken")
}

func TestRegisterValidation(t *testing.T) {
	s := newTestService(t, "open")
	cases := map[string]RegisterRequest{
		"username too short": {Username: "ab", Password: "password123"},
		"username bad chars": {Username: "yuri costa", Password: "password123"},
		"username too long":  {Username: strings.Repeat("a", 33), Password: "password123"},
		"password too short": {Username: "yuri", Password: "short"},
		"password too long":  {Username: "yuri", Password: strings.Repeat("p", 129)},
		"name too long":      {Username: "yuri", Password: "password123", Name: strings.Repeat("n", 65)},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.Register(context.Background(), req)
			wantCode(t, err, 422, "validation")
		})
	}
}

func TestRegisterClosed(t *testing.T) {
	s := newTestService(t, "closed")
	_, err := s.Register(context.Background(), RegisterRequest{Username: "yuri", Password: "password123"})
	wantCode(t, err, 403, "registration_closed")
}

func TestLogin(t *testing.T) {
	s := newTestService(t, "open")
	register(t, s, "yuri")
	sess, err := s.Login(context.Background(), LoginRequest{Username: "Yuri", Password: "password123"})
	if err != nil || sess.User.Username != "yuri" {
		t.Fatalf("sess = %+v, err = %v", sess, err)
	}
	_, err = s.Login(context.Background(), LoginRequest{Username: "yuri", Password: "wrong-password"})
	wantCode(t, err, 401, "unauthorized")
	_, err = s.Login(context.Background(), LoginRequest{Username: "ghost", Password: "password123"})
	wantCode(t, err, 401, "unauthorized")
}

func TestRefreshRotatesToken(t *testing.T) {
	s := newTestService(t, "open")
	first := register(t, s, "yuri")
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.User.ID != first.User.ID {
		t.Fatalf("refresh did not rotate: %+v", second)
	}
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken}); err != nil {
		t.Fatalf("new refresh token should work: %v", err)
	}
}

// fixedClock prende o relógio do Service em base+offset, para controlar a janela de graça e o TTL.
func fixedClock(s *Service) (advance func(time.Duration)) {
	base := time.Now()
	var offset time.Duration
	s.now = func() time.Time { return base.Add(offset) }
	return func(d time.Duration) { offset += d }
}

func TestRefreshReuseRevokesTheWholeFamily(t *testing.T) {
	s := newTestService(t, "open")
	advance := fixedClock(s)
	first := register(t, s, "yuri")
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	// reapresentar o token antigo depois da janela de graça é sinal de roubo
	advance(refreshReuseGrace + time.Second) // ainda dentro do RefreshTTL de 1h
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	// e o token novo, da mesma família, também morre
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

// Resposta da rotação perdida: o retry com o token antigo dentro da janela de graça devolve um sucessor irmão
// (token novo, mesma família) em vez de 401, e os dois ramos seguem vivos.
func TestRefreshRetryInsideGraceWindowIssuesSibling(t *testing.T) {
	s := newTestService(t, "open")
	advance := fixedClock(s)
	first := register(t, s, "yuri")
	lost, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken}) // resposta "perdida"
	if err != nil {
		t.Fatal(err)
	}
	advance(refreshReuseGrace / 2)
	retry, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatalf("retry inside the grace window must succeed: %v", err)
	}
	if retry.RefreshToken == lost.RefreshToken || retry.RefreshToken == first.RefreshToken || retry.User.ID != first.User.ID {
		t.Fatalf("retry must get a fresh sibling token: %+v", retry)
	}
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: retry.RefreshToken}); err != nil {
		t.Fatalf("sibling must work: %v", err)
	}
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: lost.RefreshToken}); err != nil {
		t.Fatalf("original successor must still work: %v", err)
	}
	// fora da janela o token antigo continua sendo reuso: revoga a família (cobre também os irmãos)
	advance(refreshReuseGrace + time.Second)
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

func TestRefreshReuseOfExpiredTokenRevokesTheFamily(t *testing.T) {
	s := newTestService(t, "open")
	advance := fixedClock(s)
	first := register(t, s, "yuri") // expira em t0+1h
	advance(50 * time.Minute)
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken}) // expira em t0+1h50
	if err != nil {
		t.Fatal(err)
	}
	advance(11 * time.Minute) // t0+1h01: o primeiro já expirou e foi usado há 11 min
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	// o irmão ainda estaria no prazo, então só morre se a família foi revogada
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

func TestConcurrentRefreshDoesNotLogOut(t *testing.T) {
	s := newTestService(t, "open")
	sess := register(t, s, "yuri")
	const n = 8
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		sesss = make([]Session, n)
		errs  = make([]error, n)
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sesss[i], errs[i] = s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken})
		}()
	}
	close(start)
	wg.Wait()

	// todos ganham um token (o primeiro rotaciona, os demais caem na janela de graça e recebem um irmão), todos distintos
	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent refresh %d failed: %v", i, err)
		}
		if seen[sesss[i].RefreshToken] {
			t.Fatal("two concurrent refreshes got the same refresh token")
		}
		seen[sesss[i].RefreshToken] = true
	}
	for _, sess := range sesss {
		if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken}); err != nil {
			t.Fatalf("every concurrent successor must work (family revoked?): %v", err)
		}
	}
}

func TestRefreshRejectsUnknownAndExpired(t *testing.T) {
	s := newTestService(t, "open")
	_, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: "nope"})
	wantCode(t, err, 401, "unauthorized")

	sess := register(t, s, "yuri")
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) } // além do RefreshTTL de 1h
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	s := newTestService(t, "open")
	first := register(t, s, "yuri")
	// rotaciona para a família ter dois tokens: o primeiro usado, o segundo vivo
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken}); err != nil {
		t.Fatal(err)
	}
	// conferido direto no banco antes de qualquer Refresh: reapresentar o token usado dispararia a
	// detecção de reuso e revogaria a família por conta própria, mascarando um logout que revoga só uma linha
	var live int
	if err := s.store.db.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live refresh tokens after logout = %d (err = %v), want 0", live, err)
	}
	// e pela API: os dois tokens da família, o usado e o vivo, respondem 401
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	if err := s.Logout(context.Background(), RefreshRequest{RefreshToken: "unknown"}); err != nil {
		t.Fatalf("logout with unknown token must not fail: %v", err)
	}
}

func TestMeAndUpdateMe(t *testing.T) {
	s := newTestService(t, "open")
	sess := register(t, s, "yuri")
	uid, _ := ids.Parse(sess.User.ID)

	me, err := s.Me(context.Background(), uid)
	if err != nil || me.Username != "yuri" {
		t.Fatalf("me = %+v, err = %v", me, err)
	}

	name, sub, hue := "Yuri C.", "codando", 200.0
	got, err := s.UpdateMe(context.Background(), uid, UpdateMeRequest{Name: &name, Subtitle: &sub, Hue: &hue})
	if err != nil || got.Name != "Yuri C." || got.Subtitle != "codando" || got.Hue == nil || *got.Hue != 200 {
		t.Fatalf("got = %+v, err = %v", got, err)
	}

	empty := ""
	got, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Subtitle: &empty})
	if err != nil || got.Subtitle != "" || got.Name != "Yuri C." {
		t.Fatalf("clearing subtitle failed: %+v, err = %v", got, err)
	}

	bad := ""
	_, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Name: &bad})
	wantCode(t, err, 422, "validation")
	badHue := 400.0
	_, err = s.UpdateMe(context.Background(), uid, UpdateMeRequest{Hue: &badHue})
	wantCode(t, err, 422, "validation")
}

func TestCreateUserOnlyMapsUsernameConflictToTaken(t *testing.T) {
	s := newTestService(t, "open")
	ctx := context.Background()
	if _, err := s.store.createUser(ctx, 42, "yuri", "Yuri", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.createUser(ctx, 43, "yuri", "Yuri", "hash"); !errors.Is(err, errUsernameTaken) {
		t.Fatalf("same username: err = %v, want errUsernameTaken", err)
	}
	// conflito na chave primária não é "username taken"
	_, err := s.store.createUser(ctx, 42, "outro", "Outro", "hash")
	if err == nil || errors.Is(err, errUsernameTaken) {
		t.Fatalf("same id, different username: err = %v, want a non-username_taken error", err)
	}
}

func countRefresh(t *testing.T, s *Service) (n int) {
	t.Helper()
	if err := s.store.db.QueryRow(context.Background(), `SELECT count(*) FROM refresh_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

// A limpeza periódica apaga expirados/usados/revogados só depois da retenção, e preserva os tokens vivos.
func TestCleanupRefreshAppliesRetention(t *testing.T) {
	s := newTestService(t, "open")
	s.cfg.RefreshRetention = 7 * 24 * time.Hour
	advance := fixedClock(s)
	ctx := context.Background()

	old := register(t, s, "yuri")                                                  // t0: expira em t0+1h
	rotated, err := s.Refresh(ctx, RefreshRequest{RefreshToken: old.RefreshToken}) // usa o primeiro, cria o segundo
	if err != nil {
		t.Fatal(err)
	}
	if n := countRefresh(t, s); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	// logo depois: nada passou da retenção
	if n, err := s.CleanupRefresh(ctx); err != nil || n != 0 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}

	// 3 dias depois: o usuário abre outra sessão e a revoga (logout); ainda dentro da retenção
	advance(3 * 24 * time.Hour)
	other := register(t, s, "ana")
	if err := s.Logout(ctx, RefreshRequest{RefreshToken: other.RefreshToken}); err != nil {
		t.Fatal(err)
	}
	// 5 dias depois (t0+8d): os dois de yuri expiraram/foram usados há mais de 7 dias; o revogado (t0+3d) ainda não
	advance(5 * 24 * time.Hour)
	if n, err := s.CleanupRefresh(ctx); err != nil || n != 2 {
		t.Fatalf("cleanup after retention = %d, %v (want the 2 rows of the first session)", n, err)
	}
	if n := countRefresh(t, s); n != 1 {
		t.Fatalf("rows left = %d, want the revoked one", n)
	}
	_ = rotated
	// mais 3 dias: o revogado também passa da retenção
	advance(3 * 24 * time.Hour)
	if n, err := s.CleanupRefresh(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup revoked = %d, %v", n, err)
	}
	if n := countRefresh(t, s); n != 0 {
		t.Fatalf("rows left = %d", n)
	}
}

// O job periódico está registrado e roda a limpeza pelo worker, apagando os velhos e preservando a sessão viva.
func TestCleanupRefreshJobRunsThroughWorker(t *testing.T) {
	s := newTestService(t, "open")
	s.cfg.RefreshRetention = time.Hour
	ctx := context.Background()
	live := register(t, s, "yuri")
	if _, err := s.store.db.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, used_at)
		SELECT 900, user_id, 900, '\x01', now() - interval '3 hours', now() - interval '4 hours' FROM refresh_tokens LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	w := jobs.NewWorker(s.store.db, ids.NewGenerator(2), jobs.Options{})
	s.RegisterJobs(w)
	if err := jobs.Enqueue(ctx, s.store.db, 1, JobCleanupRefresh, struct{}{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n, err := w.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("jobs run = %d, %v", n, err)
	}
	if n := countRefresh(t, s); n != 1 {
		t.Fatalf("rows = %d, want only the live session", n)
	}
	if _, err := s.Refresh(ctx, RefreshRequest{RefreshToken: live.RefreshToken}); err != nil {
		t.Fatalf("live session must survive the cleanup: %v", err)
	}
}
