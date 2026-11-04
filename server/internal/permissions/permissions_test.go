package permissions

import "testing"

const guildID = 100

func everyone(p Perm) Role { return Role{ID: guildID, Position: 0, Perms: p} }

func sub(userID int64, perms Perm, roles ...Role) Subject {
	return Subject{UserID: userID, Everyone: everyone(perms), Roles: roles}
}

func TestBase(t *testing.T) {
	mod := Role{ID: 1, Position: 1, Perms: ManageMessages | KickMembers}
	admin := Role{ID: 2, Position: 2, Perms: Administrator}
	tests := []struct {
		name string
		s    Subject
		want Perm
	}{
		{"só everyone", sub(1, DefaultEveryone), DefaultEveryone},
		{"união de papéis", sub(1, DefaultEveryone, mod), DefaultEveryone | ManageMessages | KickMembers},
		{"administrator vira tudo", sub(1, 0, admin), All},
		{"dono tem tudo", Subject{UserID: 1, IsOwner: true, Everyone: everyone(0)}, All},
		{"sem nada", sub(1, 0), 0},
	}
	for _, tc := range tests {
		if got := tc.s.Base(); got != tc.want {
			t.Errorf("%s: %b, queria %b", tc.name, got, tc.want)
		}
	}
}

func TestInChannel(t *testing.T) {
	rA := Role{ID: 1, Position: 1, Perms: 0}
	rB := Role{ID: 2, Position: 2, Perms: 0}
	ov := func(k TargetKind, id int64, allow, deny Perm) Override {
		return Override{Kind: k, TargetID: id, Allow: allow, Deny: deny}
	}
	tests := []struct {
		name string
		s    Subject
		ovs  []Override
		want Perm
	}{
		{"sem overrides = base", sub(1, DefaultEveryone), nil, DefaultEveryone},
		{"everyone nega ver: some o canal", sub(1, DefaultEveryone), []Override{ov(TargetRole, guildID, 0, ViewChannel)}, 0},
		{"papel devolve ver depois do deny de everyone", sub(1, DefaultEveryone, rA),
			[]Override{ov(TargetRole, guildID, 0, ViewChannel), ov(TargetRole, 1, ViewChannel, 0)},
			DefaultEveryone},
		{"allow de papel vence deny de outro papel", sub(1, DefaultEveryone, rA, rB),
			[]Override{ov(TargetRole, 1, SendMessages, 0), ov(TargetRole, 2, 0, SendMessages)},
			DefaultEveryone},
		{"deny de papel tira", sub(1, DefaultEveryone, rA), []Override{ov(TargetRole, 1, 0, SendMessages)}, DefaultEveryone &^ SendMessages},
		{"membro vence papel", sub(1, DefaultEveryone, rA),
			[]Override{ov(TargetRole, 1, 0, SendMessages), ov(TargetMember, 1, SendMessages, 0)},
			DefaultEveryone},
		{"deny do membro vence allow do papel", sub(1, DefaultEveryone, rA),
			[]Override{ov(TargetRole, 1, ManageMessages, 0), ov(TargetMember, 1, 0, ManageMessages)},
			DefaultEveryone},
		{"override de outro membro é ignorado", sub(1, DefaultEveryone), []Override{ov(TargetMember, 2, 0, ViewChannel)}, DefaultEveryone},
		{"override de papel que não tenho é ignorado", sub(1, DefaultEveryone), []Override{ov(TargetRole, 9, 0, ViewChannel)}, DefaultEveryone},
		{"allow dá permissão que a base não tem", sub(1, ViewChannel), []Override{ov(TargetRole, guildID, SendMessages, 0)}, ViewChannel | SendMessages},
		{"sem ver, nada vale", sub(1, SendMessages|ManageMessages), nil, 0},
		{"sem connect, sem speak", sub(1, ViewChannel|Speak), nil, ViewChannel},
		{"deny de connect derruba speak", sub(1, DefaultEveryone), []Override{ov(TargetRole, guildID, 0, Connect)}, DefaultEveryone &^ (Connect | Speak)},
		{"administrator ignora overrides", sub(1, 0, Role{ID: 3, Position: 3, Perms: Administrator}),
			[]Override{ov(TargetMember, 1, 0, ViewChannel)}, All},
		{"dono ignora overrides", Subject{UserID: 1, IsOwner: true, Everyone: everyone(0)},
			[]Override{ov(TargetMember, 1, 0, All)}, All},
	}
	for _, tc := range tests {
		if got := tc.s.InChannel(tc.ovs); got != tc.want {
			t.Errorf("%s: %b, queria %b", tc.name, got, tc.want)
		}
	}
}

func TestHierarchy(t *testing.T) {
	low := Role{ID: 1, Position: 1}
	mid := Role{ID: 2, Position: 2}
	high := Role{ID: 3, Position: 3}
	owner := Subject{UserID: 1, IsOwner: true, Everyone: everyone(0)}
	tests := []struct {
		name          string
		actor, target Subject
		want          bool
	}{
		{"mais alto gerencia mais baixo", sub(2, 0, high), sub(3, 0, mid), true},
		{"igual não gerencia", sub(2, 0, mid), sub(3, 0, mid), false},
		{"mais baixo não gerencia", sub(2, 0, low), sub(3, 0, high), false},
		{"com papel gerencia sem papel", sub(2, 0, low), sub(3, 0), true},
		{"sem papel não gerencia sem papel", sub(2, 0), sub(3, 0), false},
		{"dono gerencia todos", owner, sub(3, 0, high), true},
		{"ninguém gerencia o dono", sub(2, 0, high), owner, false},
		{"admin sem posição alta não gerencia", sub(2, 0, Role{ID: 4, Position: 1, Perms: Administrator}), sub(3, 0, mid), false},
	}
	for _, tc := range tests {
		if got := CanManageMember(tc.actor, tc.target); got != tc.want {
			t.Errorf("membro/%s: %v", tc.name, got)
		}
	}

	roleTests := []struct {
		name string
		a    Subject
		pos  int
		want bool
	}{
		{"abaixo", sub(2, 0, mid), 1, true},
		{"mesma posição", sub(2, 0, mid), 2, false},
		{"acima", sub(2, 0, mid), 3, false},
		{"dono qualquer", owner, 99, true},
		{"sem papel não gerencia nenhum", sub(2, 0), 1, false},
	}
	for _, tc := range roleTests {
		if got := CanManageRole(tc.a, tc.pos); got != tc.want {
			t.Errorf("papel/%s: %v", tc.name, got)
		}
	}
}

func TestCanGrantAndValid(t *testing.T) {
	if !CanGrant(ViewChannel|SendMessages, SendMessages) || CanGrant(ViewChannel, SendMessages) || !CanGrant(All, Administrator) {
		t.Error("CanGrant")
	}
	if !All.Valid() || Perm(1<<40).Valid() {
		t.Error("Valid")
	}
	s := sub(1, 0, Role{ID: 1, Position: 1}, Role{ID: 2, Position: 5})
	if s.TopPosition() != 5 || s.TopRoleID() != 2 || sub(1, 0).TopRoleID() != 0 {
		t.Error("top")
	}
}
