// Package config lê a configuração do processo a partir de variáveis de ambiente.
package config

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"

	"kurz/server/internal/platform/ids"
)

type Config struct {
	Addr         string
	DatabaseURL  string
	JWTSecret    []byte
	NodeID       int64
	ServerName   string
	Registration string // "open" ou "closed"
	S3           S3
	Voice        Voice
	RateLimit    RateLimit
	WS           WebSocket
	MetricsAddr  string // KURZ_METRICS_ADDR: listener próprio do /metrics (ex.: :9090). Vazio = /metrics no listener da API (só dev)
	TrustedProxy bool   // KURZ_TRUSTED_PROXY: confia no X-Forwarded-For (só atrás de um proxy que o sobrescreve)
	OtelEndpoint string // KURZ_OTEL_ENDPOINT: URL OTLP/HTTP do coletor (ex.: http://otel:4318); vazio desliga os traces
}

// RateLimit configura os token buckets (RNF10). O valor zero é "desligado"; Load liga com os padrões.
// Os limites são por réplica, em memória: com N réplicas o limite efetivo de um cliente é até N vezes maior.
type RateLimit struct {
	Enabled     bool    // KURZ_RATELIMIT=off desliga (loadtest)
	AuthBurst   float64 // KURZ_RATELIMIT_AUTH_BURST: rajada por IP nos endpoints de auth (padrão 20)
	AuthPerMin  float64 // KURZ_RATELIMIT_AUTH_PER_MIN: reposição por IP, tokens/minuto (padrão 20)
	WriteBurst  float64 // KURZ_RATELIMIT_WRITE_BURST: rajada por usuário nas escritas (padrão 60)
	WritePerSec float64 // KURZ_RATELIMIT_WRITE_PER_SEC: reposição por usuário, tokens/s (padrão 20)
}

// WebSocket limita o gateway. Zero = padrão do gateway (frame 10/s, rajada 20, 20 sessões por usuário).
type WebSocket struct {
	FrameRate          float64 // KURZ_WS_FRAME_RATE: frames do cliente por segundo por conexão (voice.state.set custa 4)
	FrameBurst         float64 // KURZ_WS_FRAME_BURST: rajada de frames por conexão
	MaxSessionsPerUser int     // KURZ_WS_MAX_SESSIONS_PER_USER: sessões (ao vivo ou esperando resume) por usuário e réplica
}

// Voice configura a voz. É opcional: sem Secret e SFUURL ela fica desligada (Enabled() == false), os endpoints de voz
// respondem 501 e a capability `voice` não é anunciada. Os dois precisam vir juntos.
type Voice struct {
	Secret     []byte      // KURZ_SECRET: o mesmo segredo do SFU (HS256 do token de mídia)
	SFUURL     string      // KURZ_SFU_URL: ws:// ou wss:// do /v1/media do SFU, como o CLIENTE alcança
	ICEServers []ICEServer // KURZ_ICE_SERVERS: JSON [{"urls":[...],"username":"","credential":""}]; padrão: STUN do Google
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

func (v Voice) Enabled() bool { return len(v.Secret) > 0 && v.SFUURL != "" }

// S3 configura o armazenamento de anexos. É opcional: sem Endpoint o storage fica desligado
// (Enabled() == false) e os endpoints de anexo respondem 501. Os demais campos têm padrões de dev (SeaweedFS do compose).
type S3 struct {
	Endpoint       string // host:port ou URL (http://host:port); vazio desliga o storage
	PublicEndpoint string // endereço que o CLIENTE alcança, usado nas URLs pré-assinadas; padrão: Endpoint
	Bucket         string
	AccessKey      string
	SecretKey      string
	Region         string
}

func (s S3) Enabled() bool { return s.Endpoint != "" }

// Load recebe os.Getenv (ou um fake nos testes).
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:         or(getenv("KURZ_ADDR"), ":8080"),
		DatabaseURL:  getenv("KURZ_DATABASE_URL"),
		JWTSecret:    []byte(getenv("KURZ_JWT_SECRET")),
		ServerName:   or(getenv("KURZ_SERVER_NAME"), "Kurz"),
		Registration: or(getenv("KURZ_REGISTRATION"), "open"),
	}
	c.S3 = S3{
		Endpoint:       getenv("KURZ_S3_ENDPOINT"),
		PublicEndpoint: getenv("KURZ_S3_PUBLIC_ENDPOINT"),
		Bucket:         or(getenv("KURZ_S3_BUCKET"), "kurz"),
		AccessKey:      or(getenv("KURZ_S3_ACCESS_KEY"), "kurzdev"),
		SecretKey:      or(getenv("KURZ_S3_SECRET_KEY"), "kurzdev-secret"),
		Region:         or(getenv("KURZ_S3_REGION"), "us-east-1"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("KURZ_DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("KURZ_JWT_SECRET must have at least 32 bytes")
	}
	if getenv("KURZ_NODE_ID_FROM_HOSTNAME") == "true" {
		// StatefulSet: o hostname é <nome>-<ordinal>, estável por réplica e único no conjunto.
		n, err := NodeIDFromHostname(getenv("HOSTNAME"))
		if err != nil {
			return Config{}, err
		}
		if n > ids.MaxNode {
			return Config{}, fmt.Errorf("hostname ordinal %d exceeds the max node id %d", n, ids.MaxNode)
		}
		c.NodeID = n
	} else if v := getenv("KURZ_NODE_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > ids.MaxNode {
			return Config{}, fmt.Errorf("KURZ_NODE_ID must be an integer in [0, %d]", ids.MaxNode)
		}
		c.NodeID = n
	}
	if c.Registration != "open" && c.Registration != "closed" {
		return Config{}, fmt.Errorf("KURZ_REGISTRATION must be open or closed")
	}
	c.TrustedProxy = getenv("KURZ_TRUSTED_PROXY") == "true"
	c.OtelEndpoint = getenv("KURZ_OTEL_ENDPOINT")
	c.MetricsAddr = getenv("KURZ_METRICS_ADDR")
	if err := loadRateLimit(&c, getenv); err != nil {
		return Config{}, err
	}
	if err := loadWS(&c, getenv); err != nil {
		return Config{}, err
	}
	if err := loadVoice(&c, getenv); err != nil {
		return Config{}, err
	}
	return c, nil
}

// NodeIDFromHostname extrai o sufixo numérico de "kurz-3" (o ordinal de um pod de StatefulSet).
func NodeIDFromHostname(host string) (int64, error) {
	i := strings.LastIndexByte(host, '-')
	if i < 0 || i == len(host)-1 {
		return 0, fmt.Errorf("KURZ_NODE_ID_FROM_HOSTNAME: hostname %q has no numeric suffix", host)
	}
	n, err := strconv.ParseInt(host[i+1:], 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("KURZ_NODE_ID_FROM_HOSTNAME: hostname %q has no numeric suffix", host)
	}
	return n, nil
}

func loadRateLimit(c *Config, getenv func(string) string) error {
	rl := RateLimit{Enabled: true, AuthBurst: 20, AuthPerMin: 20, WriteBurst: 60, WritePerSec: 20}
	switch getenv("KURZ_RATELIMIT") {
	case "", "on":
	case "off":
		rl.Enabled = false
	default:
		return fmt.Errorf("KURZ_RATELIMIT must be on or off")
	}
	for _, f := range []struct {
		env string
		dst *float64
	}{
		{"KURZ_RATELIMIT_AUTH_BURST", &rl.AuthBurst}, {"KURZ_RATELIMIT_AUTH_PER_MIN", &rl.AuthPerMin},
		{"KURZ_RATELIMIT_WRITE_BURST", &rl.WriteBurst}, {"KURZ_RATELIMIT_WRITE_PER_SEC", &rl.WritePerSec},
	} {
		if v := getenv(f.env); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("%s must be a positive number", f.env)
			}
			*f.dst = n
		}
	}
	c.RateLimit = rl
	return nil
}

func loadWS(c *Config, getenv func(string) string) error {
	for _, f := range []struct {
		env string
		dst *float64
	}{{"KURZ_WS_FRAME_RATE", &c.WS.FrameRate}, {"KURZ_WS_FRAME_BURST", &c.WS.FrameBurst}} {
		if v := getenv(f.env); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("%s must be a positive number", f.env)
			}
			*f.dst = n
		}
	}
	if v := getenv("KURZ_WS_MAX_SESSIONS_PER_USER"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("KURZ_WS_MAX_SESSIONS_PER_USER must be a positive integer")
		}
		c.WS.MaxSessionsPerUser = n
	}
	return nil
}

func loadVoice(c *Config, getenv func(string) string) error {
	secret, url, ice := getenv("KURZ_SECRET"), getenv("KURZ_SFU_URL"), getenv("KURZ_ICE_SERVERS")
	if secret == "" && url == "" {
		return nil // voz desligada
	}
	if secret == "" || url == "" {
		return fmt.Errorf("KURZ_SECRET and KURZ_SFU_URL must be set together")
	}
	if len(secret) < 16 {
		return fmt.Errorf("KURZ_SECRET must have at least 16 bytes")
	}
	if u, err := neturl.Parse(url); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fmt.Errorf("KURZ_SFU_URL must be a ws:// or wss:// URL")
	}
	c.Voice = Voice{Secret: []byte(secret), SFUURL: url}
	if ice != "" {
		if err := json.Unmarshal([]byte(ice), &c.Voice.ICEServers); err != nil {
			return fmt.Errorf("KURZ_ICE_SERVERS is not valid JSON: %w", err)
		}
		for _, s := range c.Voice.ICEServers {
			if len(s.URLs) == 0 {
				return fmt.Errorf("KURZ_ICE_SERVERS: every server needs urls")
			}
		}
	}
	return nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
