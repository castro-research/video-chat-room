// Package config lê a configuração do processo a partir de variáveis de ambiente.
package config

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strconv"

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
// (Enabled() == false) e os endpoints de anexo respondem 501. Os demais campos têm padrões de dev (MinIO do compose).
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
	if v := getenv("KURZ_NODE_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > ids.MaxNode {
			return Config{}, fmt.Errorf("KURZ_NODE_ID must be an integer in [0, %d]", ids.MaxNode)
		}
		c.NodeID = n
	}
	if c.Registration != "open" && c.Registration != "closed" {
		return Config{}, fmt.Errorf("KURZ_REGISTRATION must be open or closed")
	}
	if err := loadVoice(&c, getenv); err != nil {
		return Config{}, err
	}
	return c, nil
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
