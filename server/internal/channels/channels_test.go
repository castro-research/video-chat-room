package channels

import (
	"context"
	"testing"

	"kurz/server/internal/permissions"
	"kurz/server/internal/platform/dbtest"
	"kurz/server/internal/platform/httpx"
	"kurz/server/internal/platform/ids"
)

// fakeGuilds dá a cada usuário um Subject fixo, sem tocar no módulo de guilds.
type fakeGuilds map[int64]permissions.Subject

func (f fakeGuilds) Subject(_ context.Context, _, user int64) (permissions.Subject, error) {
	s, ok := f[user]
	if !ok {
		return permissions.Subject{}, httpx.ErrNotFound
	}
	return s, nil
}

func (f fakeGuilds) RolePosition(context.Context, int64, int64) (int, error) { return 1, nil }

func TestPermissionsResolveAgainstStoredOverrides(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	gen := ids.NewGenerator(1)
	const owner, alice, bob, gid = 1, 2, 3, 500
	for _, u := range []int64{owner, alice, bob} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, name, password_hash) VALUES ($1, $2, $2, 'x')`, u, "u"+ids.Format(u)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO guilds (id, name, owner_id) VALUES ($1, 'g', $2)`, gid, owner); err != nil {
		t.Fatal(err)
	}
	everyone := permissions.Role{ID: gid, Perms: permissions.DefaultEveryone}
	helpers := permissions.Role{ID: 600, Position: 1, Perms: permissions.ManageMessages}
	subs := fakeGuilds{
		owner: {UserID: owner, IsOwner: true, Everyone: everyone},
		alice: {UserID: alice, Everyone: everyone, Roles: []permissions.Role{helpers}},
		bob:   {UserID: bob, Everyone: everyone},
	}
	svc := NewService(pool, gen, subs)

	ch, err := svc.Create(ctx, owner, gid, CreateInput{Name: "x", Kind: KindText})
	if err != nil {
		t.Fatal(err)
	}
	set := func(o permissions.Override) {
		t.Helper()
		if _, err := svc.SetOverride(ctx, owner, ch.ID, o); err != nil {
			t.Fatal(err)
		}
	}
	perms := func(user int64) permissions.Perm {
		t.Helper()
		p, err := svc.Permissions(ctx, user, ch.ID)
		if err != nil {
			t.Fatalf("permissions(%d): %v", user, err)
		}
		return p
	}

	if got := perms(bob); got != permissions.DefaultEveryone {
		t.Fatalf("sem overrides = %b", got)
	}
	if got := perms(alice); !got.Has(permissions.ManageMessages) {
		t.Fatalf("alice deveria ter ManageMessages: %b", got)
	}

	// @everyone perde SendMessages, o papel helpers o recupera, bob continua sem
	set(permissions.Override{Kind: permissions.TargetRole, TargetID: gid, Deny: permissions.SendMessages})
	set(permissions.Override{Kind: permissions.TargetRole, TargetID: 600, Allow: permissions.SendMessages})
	if perms(bob).Has(permissions.SendMessages) || !perms(alice).Has(permissions.SendMessages) {
		t.Fatal("deny de everyone + allow do papel")
	}
	// override de membro vence tudo
	set(permissions.Override{Kind: permissions.TargetMember, TargetID: alice, Deny: permissions.SendMessages})
	set(permissions.Override{Kind: permissions.TargetMember, TargetID: bob, Allow: permissions.SendMessages})
	if perms(alice).Has(permissions.SendMessages) || !perms(bob).Has(permissions.SendMessages) {
		t.Fatal("override de membro")
	}
	// o dono nunca perde nada
	set(permissions.Override{Kind: permissions.TargetMember, TargetID: owner, Deny: permissions.All &^ permissions.Administrator})
	if perms(owner) != permissions.All {
		t.Fatal("dono ignora overrides")
	}
	// sem ver, o canal some para o usuário
	set(permissions.Override{Kind: permissions.TargetMember, TargetID: bob, Deny: permissions.ViewChannel})
	if _, err := svc.Permissions(ctx, bob, ch.ID); err == nil {
		t.Fatal("bob não deveria ver o canal")
	}
	// quem não é membro também não
	if _, err := svc.Permissions(ctx, 99, ch.ID); err == nil {
		t.Fatal("não-membro não vê")
	}
}
