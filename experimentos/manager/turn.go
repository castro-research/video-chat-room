package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Cópia idêntica em manager/turn.go e sfu/turn.go (módulos separados).

type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// turnSource gera credenciais temporárias no TURN gerenciado da Cloudflare (Realtime TURN) e as reaproveita até a
// metade do TTL: quem recebe uma credencial tem sempre pelo menos ttl/2 de validade.
// ponytail: chamada mais longa que ttl/2 pode perder o relay quando a credencial expira.
type turnSource struct {
	keyID, token string
	ttl          time.Duration
	http         *http.Client

	mu        sync.Mutex
	servers   []iceServer
	renewAt   time.Time
	expiresAt time.Time
}

func newTurnSource(keyID, token string) *turnSource {
	if keyID == "" || token == "" {
		return nil
	}
	return &turnSource{keyID: keyID, token: token, ttl: 24 * time.Hour, http: &http.Client{Timeout: 5 * time.Second}}
}

func (t *turnSource) get(ctx context.Context) ([]iceServer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.servers != nil && now.Before(t.renewAt) {
		return t.servers, nil
	}
	servers, err := t.generate(ctx)
	if err != nil {
		if t.servers != nil && now.Before(t.expiresAt) {
			return t.servers, nil // a anterior ainda vale; tenta renovar na próxima chamada
		}
		return nil, err
	}
	t.servers, t.renewAt, t.expiresAt = servers, now.Add(t.ttl/2), now.Add(t.ttl)
	return servers, nil
}

func (t *turnSource) generate(ctx context.Context) ([]iceServer, error) {
	body, _ := json.Marshal(map[string]int64{"ttl": int64(t.ttl.Seconds())})
	url := "https://rtc.live.cloudflare.com/v1/turn/keys/" + t.keyID + "/credentials/generate-ice-servers"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("turn cloudflare: %s", resp.Status)
	}
	var out struct {
		IceServers []iceServer `json:"iceServers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.IceServers) == 0 {
		return nil, fmt.Errorf("turn cloudflare: resposta sem iceServers")
	}
	return out.IceServers, nil
}
