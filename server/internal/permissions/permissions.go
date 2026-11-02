// Package permissions resolve o que um membro pode fazer numa guild e num canal. É puro: sem I/O,
// só recebe papéis e overrides já carregados. A resolução segue o Discord:
// base (@everyone + papéis) -> override de @everyone -> overrides de papéis -> override do membro.
package permissions

import "math"

// Perm é um bitmask de permissões.
type Perm uint64

const (
	ViewChannel Perm = 1 << iota
	SendMessages
	ManageMessages
	ManageChannels
	CreateInvite
	KickMembers
	BanMembers
	Connect
	Speak
	ManageRoles
	Administrator
)

// All é todas as permissões definidas.
const All = Administrator<<1 - 1

// DefaultEveryone é o que o papel @everyone de uma guild nova concede.
const DefaultEveryone = ViewChannel | SendMessages | CreateInvite | Connect | Speak

// Has diz se p contém todos os bits de want.
func (p Perm) Has(want Perm) bool { return p&want == want }

// Valid diz se p só usa bits conhecidos.
func (p Perm) Valid() bool { return p&^All == 0 }

// Role é um papel da guild. Position maior = mais poder; @everyone tem posição 0.
type Role struct {
	ID       int64
	Position int
	Perms    Perm
}

// Subject é um membro com os papéis dele. Everyone é o papel @everyone (id = id da guild);
// Roles são os demais papéis atribuídos ao membro.
type Subject struct {
	UserID   int64
	IsOwner  bool
	Everyone Role
	Roles    []Role
}

// Base são as permissões na guild, sem overrides de canal. Dono e Administrator têm tudo.
func (s Subject) Base() Perm {
	if s.IsOwner {
		return All
	}
	p := s.Everyone.Perms
	for _, r := range s.Roles {
		p |= r.Perms
	}
	if p.Has(Administrator) {
		return All
	}
	return p
}

// TopPosition é a posição do papel mais alto do membro (0 se só tem @everyone). O dono está acima de tudo.
func (s Subject) TopPosition() int {
	if s.IsOwner {
		return math.MaxInt
	}
	top := 0
	for _, r := range s.Roles {
		top = max(top, r.Position)
	}
	return top
}

// TopRoleID é o id do papel mais alto do membro, ou 0 se não tem nenhum além de @everyone.
func (s Subject) TopRoleID() int64 {
	var id int64
	top := 0
	for _, r := range s.Roles {
		if r.Position > top {
			top, id = r.Position, r.ID
		}
	}
	return id
}

// TargetKind diz a que um override se aplica.
type TargetKind string

const (
	TargetRole   TargetKind = "role"
	TargetMember TargetKind = "member"
)

// Override é uma regra allow/deny de um canal para um papel ou membro.
type Override struct {
	Kind     TargetKind
	TargetID int64
	Allow    Perm
	Deny     Perm
}

// InChannel resolve as permissões no canal. Sem ViewChannel nada vale (0); sem Connect não há Speak.
func (s Subject) InChannel(overrides []Override) Perm {
	base := s.Base()
	if base.Has(Administrator) { // inclui o dono
		return All
	}
	p := base

	for _, o := range overrides {
		if o.Kind == TargetRole && o.TargetID == s.Everyone.ID {
			p = p&^o.Deny | o.Allow
		}
	}

	var allow, deny Perm
	for _, o := range overrides {
		if o.Kind != TargetRole {
			continue
		}
		for _, r := range s.Roles {
			if r.ID == o.TargetID {
				allow |= o.Allow
				deny |= o.Deny
			}
		}
	}
	p = p&^deny | allow

	for _, o := range overrides {
		if o.Kind == TargetMember && o.TargetID == s.UserID {
			p = p&^o.Deny | o.Allow
		}
	}

	if !p.Has(ViewChannel) {
		return 0
	}
	if !p.Has(Connect) {
		p &^= Speak
	}
	return p
}

// CanManageMember: o dono gerencia todos (menos ele mesmo, que o chamador trata); os demais só quem
// tem posição estritamente abaixo da sua. Ninguém gerencia o dono.
func CanManageMember(actor, target Subject) bool {
	if target.IsOwner {
		return false
	}
	if actor.IsOwner {
		return true
	}
	return actor.TopPosition() > target.TopPosition()
}

// CanManageRole: só se gerencia papel de posição estritamente abaixo da sua (o dono gerencia todos).
func CanManageRole(actor Subject, rolePosition int) bool {
	return actor.IsOwner || actor.TopPosition() > rolePosition
}

// CanGrant: ninguém concede bits que não tem (o dono e Administrator têm tudo).
func CanGrant(actorPerms, wanted Perm) bool { return actorPerms&wanted == wanted }
