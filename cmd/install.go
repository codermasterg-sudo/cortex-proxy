package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortex-io/cortex-proxy/config"
)

// RunInstall creates the config directory and writes a template config.yaml if one
// doesn't already exist. Run this once before starting the proxy for the first time.
func RunInstall(args []string) {
	configPath := config.DefaultPath()
	configDir := filepath.Dir(configPath)

	if err := os.MkdirAll(configDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config directory %s: %v\n", configDir, err)
		os.Exit(1)
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config file already exists: %s\n", configPath)
		fmt.Println("Edit it to update your settings.")
		return
	}

	template := `# cortex-proxy configuration

cortex:
  # Cortex platform API key (ctxp_sk_...).
  # Can also be set via --api-key flag or CORTEX_API_KEY env var (higher priority).
  api_key: ""

  # Cortex platform URL. Leave empty to use the default (https://api.cortex.io).
  # For local development: http://localhost:8000
  platform_url: ""

upstream:
  # LLM provider base URL.
  # The proxy appends the incoming request path (/v1/chat/completions, etc.) to this.
  base_url: "https://api.openai.com"

  # LLM API key (optional).
  # If set, this key replaces the Authorization header forwarded to the upstream.
  # If left empty, the agent's own Authorization header is passed through unchanged.
  api_key: ""
`

	if err := os.WriteFile(configPath, []byte(template), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write config file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config file created: %s\n\n", configPath)
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit the config file and set upstream.base_url to your LLM provider.")
	fmt.Println("  2. Point your agent at the proxy:")
	fmt.Println("       OPENAI_BASE_URL=http://localhost:7898")
	fmt.Println("  3. Start:")
	fmt.Println("       cortex-proxy start --api-key=<your-cortex-api-key>")
}
