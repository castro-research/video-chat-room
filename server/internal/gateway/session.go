package gateway

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

type ringEntry struct {
	seq   uint64
	frame []byte
}

// session é o estado que sobrevive à conexão: o seq, o buffer circular dos últimos eventos para resume
// e as guilds do usuário. Eventos continuam entrando no buffer enquanto não há conexão.
type session struct {
	id     string
	userID int64

	mu         sync.Mutex
	guilds     map[int64]struct{}
	seq        uint64
	ring       []ringEntry // circular, capacidade fixa
	ringStart  int         // posição do mais antigo
	ringLen    int
	conn       *conn
	detachedAt time.Time
}

func newSession(userID int64, ringSize int) *session {
	return &session{
		id: newSessionID(), userID: userID, guilds: map[int64]struct{}{},
		ring: make([]ringEntry, ringSize), detachedAt: time.Now(),
	}
}

func (s *session) inGuild(guild int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.guilds[guild]
	return ok
}

func (s *session) addGuild(guild int64) {
	s.mu.Lock()
	s.guilds[guild] = struct{}{}
	s.mu.Unlock()
}

func (s *session) removeGuild(guild int64) {
	s.mu.Lock()
	delete(s.guilds, guild)
	s.mu.Unlock()
}

// deliver numera o evento, guarda no buffer e, se há conexão, enfileira. Devolve a conexão cuja fila
// estourou (o chamador a derruba, fora do lock), ou nil.
func (s *session) deliver(typ string, d json.RawMessage) (slow *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	f := encodeDispatch(typ, s.seq, d)
	if s.ringLen < len(s.ring) {
		s.ring[(s.ringStart+s.ringLen)%len(s.ring)] = ringEntry{s.seq, f}
		s.ringLen++
	} else {
		s.ring[s.ringStart] = ringEntry{s.seq, f}
		s.ringStart = (s.ringStart + 1) % len(s.ring)
	}
	if s.conn != nil && !s.conn.enqueue(f) {
		return s.conn
	}
	return nil
}

// since devolve os frames com seq > after. ok = false se o buffer já descartou algum deles
// (ou se after é do futuro): o cliente precisa de invalid_session.
func (s *session) sinceLocked(after uint64) (frames [][]byte, ok bool) {
	if after > s.seq {
		return nil, false
	}
	oldest := s.seq + 1 - uint64(s.ringLen) // seq do mais antigo guardado (seq+1 se vazio)
	if after+1 < oldest {
		return nil, false
	}
	for i := 0; i < s.ringLen; i++ {
		e := s.ring[(s.ringStart+i)%len(s.ring)]
		if e.seq > after {
			frames = append(frames, e.frame)
		}
	}
	return frames, true
}

// attach liga a conexão à sessão, derrubando a anterior (se houver), e enfileira first seguido de frames.
// Devolve false se a fila não comporta tudo.
func (s *session) attach(c *conn, first []byte, frames [][]byte) (old *conn, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(frames)+1 > cap(c.out) {
		return nil, false
	}
	old = s.conn
	s.conn = c
	c.enqueue(first)
	for _, f := range frames {
		c.enqueue(f)
	}
	return old, true
}

func (s *session) detach(c *conn) {
	s.mu.Lock()
	if s.conn == c {
		s.conn = nil
		s.detachedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *session) expired(now time.Time, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn == nil && now.Sub(s.detachedAt) > ttl
}

// encodeDispatch monta {"op":"dispatch","t":...,"seq":...,"d":...} sem re-serializar o payload.
func encodeDispatch(typ string, seq uint64, d json.RawMessage) []byte {
	t, _ := json.Marshal(typ)
	b := make([]byte, 0, len(d)+len(t)+48)
	b = append(b, `{"op":"dispatch","t":`...)
	b = append(b, t...)
	b = append(b, `,"seq":`...)
	b = strconv.AppendUint(b, seq, 10)
	b = append(b, `,"d":`...)
	b = append(b, d...)
	return append(b, '}')
}
