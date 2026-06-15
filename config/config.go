package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Upstream UpstreamConfig `yaml:"upstream"`
	Cortex   CortexConfig   `yaml:"cortex"`
}

type UpstreamConfig struct {
	// BaseURL is the LLM provider's base URL, e.g. "https://api.openai.com".
	// The proxy appends the request path (/v1/chat/completions, etc.) to this.
	BaseURL string `yaml:"base_url"`

	// APIKey, if set, replaces the Authorization header sent to the upstream.
	// Leave empty to pass through the agent's own Authorization header unchanged.
	APIKey string `yaml:"api_key"`
}

type CortexConfig struct {
	// APIKey is the Cortex platform API key (ctxp_sk_...).
	// Can also be set via --api-key flag or CORTEX_API_KEY env var (higher priority).
	APIKey string `yaml:"api_key"`

	// PlatformURL overrides the default platform address (https://api.cortex.io).
	// Can also be set via --platform flag or CORTEX_PLATFORM_URL env var (higher priority).
	PlatformURL string `yaml:"platform_url"`
}

// DefaultPath returns the config file path: ~/cortex-proxy/config.yaml
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "cortex-proxy", "config.yaml")
}

// Load reads the config file at path. Returns an empty Config (not an error)
// if the file does not exist, so callers can still apply flag/env overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}
