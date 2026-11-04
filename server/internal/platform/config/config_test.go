package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"KURZ_DATABASE_URL": "postgres://x",
		"KURZ_JWT_SECRET":   strings.Repeat("s", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.NodeID != 0 || c.ServerName != "Kurz" || c.Registration != "open" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if string(c.JWTSecret) != strings.Repeat("s", 32) || c.DatabaseURL != "postgres://x" {
		t.Fatalf("unexpected required fields: %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	m := valid()
	m["KURZ_ADDR"] = ":9000"
	m["KURZ_NODE_ID"] = "7"
	m["KURZ_SERVER_NAME"] = "Casa"
	m["KURZ_REGISTRATION"] = "closed"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9000" || c.NodeID != 7 || c.ServerName != "Casa" || c.Registration != "closed" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing database url": func(m map[string]string) { delete(m, "KURZ_DATABASE_URL") },
		"short secret":         func(m map[string]string) { m["KURZ_JWT_SECRET"] = "curto" },
		"node not a number":    func(m map[string]string) { m["KURZ_NODE_ID"] = "abc" },
		"node out of range":    func(m map[string]string) { m["KURZ_NODE_ID"] = "1024" },
		"unknown registration": func(m map[string]string) { m["KURZ_REGISTRATION"] = "weird" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid()
			mutate(m)
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadS3(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil || c.S3.Enabled() || c.S3.Bucket != "kurz" || c.S3.Region != "us-east-1" || c.S3.AccessKey == "" {
		t.Fatalf("s3 off by default: %+v %v", c.S3, err)
	}
	m := valid()
	m["KURZ_S3_ENDPOINT"], m["KURZ_S3_BUCKET"], m["KURZ_S3_PUBLIC_ENDPOINT"] = "localhost:9000", "b", "http://cdn.example"
	c, err = Load(env(m))
	if err != nil || !c.S3.Enabled() || c.S3.Bucket != "b" || c.S3.PublicEndpoint != "http://cdn.example" {
		t.Fatalf("s3 on: %+v %v", c.S3, err)
	}
}

func TestLoadVoice(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil || c.Voice.Enabled() {
		t.Fatalf("voice off by default: %+v %v", c.Voice, err)
	}
	m := valid()
	m["KURZ_SECRET"], m["KURZ_SFU_URL"] = strings.Repeat("v", 16), "ws://sfu:8081/v1/media"
	m["KURZ_ICE_SERVERS"] = `[{"urls":["turn:t:3478"],"username":"u","credential":"c"}]`
	c, err = Load(env(m))
	if err != nil || !c.Voice.Enabled() || len(c.Voice.ICEServers) != 1 || c.Voice.ICEServers[0].Username != "u" {
		t.Fatalf("voice on: %+v %v", c.Voice, err)
	}
}

func TestLoadVoiceErrors(t *testing.T) {
	cases := map[string]func(map[string]string){
		"secret without url": func(m map[string]string) { delete(m, "KURZ_SFU_URL") },
		"url without secret": func(m map[string]string) { delete(m, "KURZ_SECRET") },
		"short secret":       func(m map[string]string) { m["KURZ_SECRET"] = "curto" },
		"http url":           func(m map[string]string) { m["KURZ_SFU_URL"] = "http://sfu/v1/media" },
		"bad ice json":       func(m map[string]string) { m["KURZ_ICE_SERVERS"] = "{" },
		"ice without urls":   func(m map[string]string) { m["KURZ_ICE_SERVERS"] = `[{}]` },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid()
			m["KURZ_SECRET"], m["KURZ_SFU_URL"] = strings.Repeat("v", 16), "wss://sfu/v1/media"
			mutate(m)
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestNodeIDFromHostname(t *testing.T) {
	for host, want := range map[string]int64{"kurz-0": 0, "kurz-2": 2, "my-app-17": 17} {
		m := valid()
		m["KURZ_NODE_ID_FROM_HOSTNAME"] = "true"
		m["HOSTNAME"] = host
		m["KURZ_NODE_ID"] = "999" // o hostname manda
		c, err := Load(env(m))
		if err != nil || c.NodeID != want {
			t.Fatalf("%s: node=%d err=%v", host, c.NodeID, err)
		}
	}
	for _, host := range []string{"", "kurz", "kurz-", "kurz-abc", "kurz-99999"} {
		m := valid()
		m["KURZ_NODE_ID_FROM_HOSTNAME"] = "true"
		m["HOSTNAME"] = host
		if _, err := Load(env(m)); err == nil {
			t.Fatalf("hostname %q should be rejected", host)
		}
	}
}

func TestRateLimitConfig(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil || !c.RateLimit.Enabled || c.RateLimit.WritePerSec != 20 || c.TrustedProxy || c.OtelEndpoint != "" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	m := valid()
	m["KURZ_RATELIMIT"] = "off"
	m["KURZ_RATELIMIT_AUTH_BURST"] = "5"
	m["KURZ_TRUSTED_PROXY"] = "true"
	m["KURZ_OTEL_ENDPOINT"] = "http://otel:4318"
	c, err = Load(env(m))
	if err != nil || c.RateLimit.Enabled || c.RateLimit.AuthBurst != 5 || !c.TrustedProxy || c.OtelEndpoint != "http://otel:4318" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	for k, v := range map[string]string{"KURZ_RATELIMIT": "maybe", "KURZ_RATELIMIT_WRITE_BURST": "-1", "KURZ_RATELIMIT_AUTH_PER_MIN": "x"} {
		m := valid()
		m[k] = v
		if _, err := Load(env(m)); err == nil {
			t.Fatalf("%s=%s should fail", k, v)
		}
	}
}

func TestWebSocketLimitsConfig(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil || c.WS != (WebSocket{}) {
		t.Fatalf("defaults (zero = gateway default): %+v %v", c.WS, err)
	}
	m := valid()
	m["KURZ_WS_FRAME_RATE"], m["KURZ_WS_FRAME_BURST"], m["KURZ_WS_MAX_SESSIONS_PER_USER"] = "5", "8", "3"
	c, err = Load(env(m))
	if err != nil || c.WS != (WebSocket{FrameRate: 5, FrameBurst: 8, MaxSessionsPerUser: 3}) {
		t.Fatalf("overrides: %+v %v", c.WS, err)
	}
	for k, v := range map[string]string{"KURZ_WS_FRAME_RATE": "0", "KURZ_WS_FRAME_BURST": "x", "KURZ_WS_MAX_SESSIONS_PER_USER": "-2"} {
		m := valid()
		m[k] = v
		if _, err := Load(env(m)); err == nil {
			t.Fatalf("%s=%s should fail", k, v)
		}
	}
}

func TestMetricsAddr(t *testing.T) {
	if c, err := Load(env(valid())); err != nil || c.MetricsAddr != "" {
		t.Fatalf("default must be empty (metrics on the API listener): %+v %v", c.MetricsAddr, err)
	}
	m := valid()
	m["KURZ_METRICS_ADDR"] = ":9090"
	if c, err := Load(env(m)); err != nil || c.MetricsAddr != ":9090" {
		t.Fatalf("%+v %v", c.MetricsAddr, err)
	}
}
