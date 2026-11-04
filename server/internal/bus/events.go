package bus

// Tipos de evento. O nome é o `t` que o gateway entrega ao cliente.
//
// Escopo de entrega no gateway:
//   - com ChannelID (message.*, reaction.*, voice.state, channel.create/update/permissions): só quem tem ViewChannel no canal;
//   - sem ChannelID, com GuildID: todos os membros da guild;
//   - guild.create: só o usuário de payload.userId.
const (
	MessageCreate  = "message.create"
	MessageUpdate  = "message.update"
	MessageDelete  = "message.delete"
	ReactionAdd    = "reaction.add"
	ReactionRemove = "reaction.remove"

	GuildCreate = "guild.create" // payload.userId: o dono, que passa a ver a guild
	GuildUpdate = "guild.update" // troca de dono
	GuildDelete = "guild.delete"

	MemberJoin   = "member.join"
	MemberLeave  = "member.leave" // payload.reason: leave, kick ou ban
	MemberUpdate = "member.update"

	RoleCreate = "role.create"
	RoleUpdate = "role.update"
	RoleDelete = "role.delete"

	ChannelCreate      = "channel.create"
	ChannelUpdate      = "channel.update"
	ChannelDelete      = "channel.delete"
	ChannelPermissions = "channel.permissions" // override criado, alterado ou removido

	// VoiceState: alguém entrou/saiu/mudou de canal de voz ou mudou mute/câmera/tela. Tem ChannelID (só quem vê o
	// canal recebe). Não altera permissões, então não invalida o cache do gateway.
	VoiceState = "voice.state"
)

// InvalidatesPermissions diz se o evento pode mudar o que alguém vê: o gateway descarta o cache
// de permissões da guild antes de rotear o evento seguinte (RNF7).
func InvalidatesPermissions(typ string) bool {
	switch typ {
	case GuildUpdate, GuildDelete, MemberJoin, MemberLeave, MemberUpdate,
		RoleCreate, RoleUpdate, RoleDelete,
		ChannelCreate, ChannelUpdate, ChannelDelete, ChannelPermissions:
		return true
	}
	return false
}
