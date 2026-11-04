package gateway

import (
	"sync"
	"time"
)

type viewKey struct{ user, channel int64 }

type viewEntry struct {
	ok      bool
	expires time.Time
}

// permCache guarda "usuário vê o canal" por guild. Os eventos de papel, membro, canal e override
// (bus.InvalidatesPermissions) esvaziam a guild inteira, em qualquer réplica, pois cada gateway
// vê os mesmos eventos (RNF7). O TTL é só rede de segurança.
type permCache struct {
	mu  sync.Mutex
	ttl time.Duration
	now func() time.Time
	m   map[int64]map[viewKey]viewEntry
}

func newPermCache(ttl time.Duration) *permCache {
	return &permCache{ttl: ttl, now: time.Now, m: map[int64]map[viewKey]viewEntry{}}
}

func (c *permCache) get(guild, user, channel int64) (ok, hit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.m[guild][viewKey{user, channel}]
	if !found || !c.now().Before(e.expires) {
		return false, false
	}
	return e.ok, true
}

func (c *permCache) put(guild, user, channel int64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.m[guild]
	if g == nil {
		g = map[viewKey]viewEntry{}
		c.m[guild] = g
	}
	g[viewKey{user, channel}] = viewEntry{ok, c.now().Add(c.ttl)}
}

func (c *permCache) invalidateGuild(guild int64) {
	c.mu.Lock()
	delete(c.m, guild)
	c.mu.Unlock()
}
