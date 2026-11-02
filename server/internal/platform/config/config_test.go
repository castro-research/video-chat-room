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
