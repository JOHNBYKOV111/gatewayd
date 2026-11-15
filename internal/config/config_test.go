package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_DefaultsApplied(t *testing.T) {
	path := writeTemp(t, "database:\n  dsn: \"postgres://from-yaml\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default Listen = %q, want :8080", cfg.Listen)
	}
	if cfg.Auth.MasterKeyEnv != "GW_MASTER_KEY" {
		t.Errorf("default MasterKeyEnv = %q, want GW_MASTER_KEY", cfg.Auth.MasterKeyEnv)
	}
	if cfg.Database.DSN != "postgres://from-yaml" {
		t.Errorf("DSN = %q, want postgres://from-yaml", cfg.Database.DSN)
	}
}

func TestLoad_EnvOverridesDatabaseDSN(t *testing.T) {
	path := writeTemp(t, "database:\n  dsn: \"postgres://from-yaml\"\n")
	t.Setenv("GW_DATABASE_DSN", "postgres://from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSN != "postgres://from-env" {
		t.Errorf("DSN = %q, want postgres://from-env", cfg.Database.DSN)
	}
}

func TestLoad_EnvOverridesListen(t *testing.T) {
	path := writeTemp(t, "listen: \":8080\"\n")
	t.Setenv("GW_LISTEN", ":9090")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", cfg.Listen)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load(missing) = nil error, want error")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTemp(t, "listen: [unclosed\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load(invalid yaml) = nil error, want error")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	path := writeTemp(t, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(empty): %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default Listen = %q, want :8080", cfg.Listen)
	}
	if cfg.Auth.MasterKeyEnv != "GW_MASTER_KEY" {
		t.Errorf("default MasterKeyEnv = %q, want GW_MASTER_KEY", cfg.Auth.MasterKeyEnv)
	}
}

func TestLoad_FullConfig(t *testing.T) {
	path := writeTemp(t, `
listen: ":9090"
database:
  dsn: "postgres://full"
providers:
  - name: local-ollama
    type: openai
    base_url: "http://localhost:11434/v1"
    api_key_env: OLLAMA_KEY
models:
  - name: "llama3:8b"
    routes: [local-ollama]
    price: { input: 1.5, output: 2.5 }
auth:
  virtual_keys: true
  master_key_env: GW_CUSTOM
limits:
  default_rpm: 120
  default_tpm: 200000
cache:
  enabled: true
  ttl: 2h
log:
  store_prompts: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":9090" {
		t.Fatalf("Listen = %q", cfg.Listen)
	}
	if cfg.Database.DSN != "postgres://full" {
		t.Fatalf("DSN = %q", cfg.Database.DSN)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "local-ollama" {
		t.Fatalf("Providers = %+v", cfg.Providers)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].Price.Input != 1.5 {
		t.Fatalf("Models = %+v", cfg.Models)
	}
	if !cfg.Auth.VirtualKeys || cfg.Auth.MasterKeyEnv != "GW_CUSTOM" {
		t.Fatalf("Auth = %+v", cfg.Auth)
	}
	if cfg.Limits.DefaultRPM != 120 || cfg.Limits.DefaultTPM != 200000 {
		t.Fatalf("Limits = %+v", cfg.Limits)
	}
	if !cfg.Cache.Enabled || cfg.Cache.TTL != "2h" {
		t.Fatalf("Cache = %+v", cfg.Cache)
	}
	if !cfg.Log.StorePrompts {
		t.Fatal("Log.StorePrompts = false, want true")
	}
}
