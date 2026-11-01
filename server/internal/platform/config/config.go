// Package config lê a configuração do processo a partir de variáveis de ambiente.
package config

import (
	"fmt"
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
}

// Load recebe os.Getenv (ou um fake nos testes).
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Addr:         or(getenv("KURZ_ADDR"), ":8080"),
		DatabaseURL:  getenv("KURZ_DATABASE_URL"),
		JWTSecret:    []byte(getenv("KURZ_JWT_SECRET")),
		ServerName:   or(getenv("KURZ_SERVER_NAME"), "Kurz"),
		Registration: or(getenv("KURZ_REGISTRATION"), "open"),
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
	return c, nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
