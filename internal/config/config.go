package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Database struct {
	DSN string `yaml:"dsn"`
}

type Provider struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

type Price struct {
	Input  float64 `yaml:"input"`
	Output float64 `yaml:"output"`
}

type Model struct {
	Name   string   `yaml:"name"`
	Routes []string `yaml:"routes"`
	Price  Price    `yaml:"price"`
}

type Auth struct {
	VirtualKeys  bool   `yaml:"virtual_keys"`
	MasterKeyEnv string `yaml:"master_key_env"`
}

type Limits struct {
	DefaultRPM int `yaml:"default_rpm"`
	DefaultTPM int `yaml:"default_tpm"`
}

type Cache struct {
	Enabled bool   `yaml:"enabled"`
	TTL     string `yaml:"ttl"`
}

type Log struct {
	StorePrompts bool `yaml:"store_prompts"`
}

type Config struct {
	Listen    string     `yaml:"listen"`
	Database  Database   `yaml:"database"`
	Providers []Provider `yaml:"providers"`
	Models    []Model    `yaml:"models"`
	Auth      Auth       `yaml:"auth"`
	Limits    Limits     `yaml:"limits"`
	Cache     Cache      `yaml:"cache"`
	Log       Log        `yaml:"log"`
}

func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config: empty path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return parse(raw)
}

func parse(raw []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: parse yaml: %w", err)
	}
	applyEnvOverrides(&c)
	applyDefaults(&c)
	if err := validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyEnvOverrides(c *Config) {
	if v := os.Getenv("GW_DATABASE_DSN"); v != "" {
		c.Database.DSN = v
	}
	if v := os.Getenv("GW_LISTEN"); v != "" {
		c.Listen = v
	}
}

func applyDefaults(c *Config) {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Auth.MasterKeyEnv == "" {
		c.Auth.MasterKeyEnv = "GW_MASTER_KEY"
	}
}

func validate(c *Config) error {
	if c.Listen == "" {
		return errors.New("config: listen is empty")
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("config: providers[%d]: name is empty", i)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("config: providers: duplicate name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	for i, m := range c.Models {
		if m.Name == "" {
			return fmt.Errorf("config: models[%d]: name is empty", i)
		}
	}
	return nil
}
