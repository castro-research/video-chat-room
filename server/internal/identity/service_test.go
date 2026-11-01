package identity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestRefreshReuseInsideGraceWindowKeepsTheFamily(t *testing.T) {
	s := newTestService(t, "open")
	advance := fixedClock(s)
	first := register(t, s, "yuri")
	second, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	// retry/corrida logo depois da rotação: 401, mas sem derrubar a sessão
	advance(refreshReuseGrace / 2)
	_, err = s.Refresh(context.Background(), RefreshRequest{RefreshToken: first.RefreshToken})
	wantCode(t, err, 401, "unauthorized")
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: second.RefreshToken}); err != nil {
		t.Fatalf("sibling token must survive a reuse inside the grace window: %v", err)
	}
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

	var winner *Session
	for i, err := range errs {
		if err == nil {
			if winner != nil {
				t.Fatal("more than one concurrent refresh succeeded")
			}
			winner = &sesss[i]
			continue
		}
		wantCode(t, err, 401, "unauthorized")
	}
	if winner == nil {
		t.Fatal("no concurrent refresh succeeded")
	}
	if _, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: winner.RefreshToken}); err != nil {
		t.Fatalf("winner's refresh token must still work (family revoked?): %v", err)
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
	sess := register(t, s, "yuri")
	if err := s.Logout(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Refresh(context.Background(), RefreshRequest{RefreshToken: sess.RefreshToken})
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
