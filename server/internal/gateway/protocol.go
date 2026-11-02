package gateway

import "encoding/json"

// Opcodes do protocolo (campo `op`). Ver contract/gateway.md.
const (
	opHello          = "hello"           // servidor: primeiro frame
	opIdentify       = "identify"        // cliente: autentica uma sessão nova
	opReady          = "ready"           // servidor: sessão criada
	opResume         = "resume"          // cliente: retoma uma sessão
	opResumed        = "resumed"         // servidor: retomada aceita; os eventos perdidos vêm em seguida
	opInvalidSession = "invalid_session" // servidor: não dá para retomar; faça identify e refaça o fetch
	opHeartbeat      = "heartbeat"       // cliente
	opHeartbeatAck   = "heartbeat_ack"   // servidor
	opDispatch       = "dispatch"        // servidor: evento com t e seq
)

// Códigos de fechamento (faixa 4000-4999 é da aplicação).
const (
	CloseAuthFailed       = 4001 // token inválido ou expirado: renove e reconecte
	CloseBadFrame         = 4002 // frame ou opcode inválido
	CloseIdentifyTimeout  = 4003 // nada de identify/resume a tempo
	CloseSlowConsumer     = 4008 // fila de saída estourou (RNF9): reconecte e faça resume
	CloseSessionMoved     = 4009 // a sessão foi retomada por outra conexão
	CloseHeartbeatTimeout = 4010 // silêncio por mais de 2 intervalos: reconecte e faça resume
)

type frame struct {
	Op  string          `json:"op"`
	T   string          `json:"t,omitempty"`
	Seq uint64          `json:"seq,omitempty"`
	D   json.RawMessage `json:"d,omitempty"`
}

type helloData struct {
	HeartbeatInterval int64 `json:"heartbeatInterval"` // ms
}

type identifyData struct {
	Token string `json:"token"`
}

type resumeData struct {
	Token     string `json:"token"`
	SessionID string `json:"sessionId"`
	Seq       uint64 `json:"seq"`
}

type userData struct {
	ID string `json:"id"`
}

type guildData struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnerID string `json:"ownerId"`
}

type readyData struct {
	SessionID         string      `json:"sessionId"`
	User              userData    `json:"user"`
	Guilds            []guildData `json:"guilds"`
	HeartbeatInterval int64       `json:"heartbeatInterval"`
}
