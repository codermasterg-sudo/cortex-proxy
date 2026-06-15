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
# All values here can be overridden by CLI flags or environment variables.
# Priority: CLI flag > environment variable > this file > built-in default.

listen:
  # Network interface to bind. Leave empty to listen on all interfaces (0.0.0.0).
  # CLI: --host=127.0.0.1
  host: ""

  # TCP port to listen on. Default: 7898.
  # CLI: --port=7898
  port: 7898

cortex:
  # Cortex platform API key (ctxp_sk_...).
  # CLI: --api-key=ctxp_sk_...   env: CORTEX_API_KEY
  api_key: ""

  # Cortex platform URL. Leave empty to use the default (https://api.cortex.io).
  # For local development: http://localhost:8000
  # CLI: --platform=https://api.cortex.io   env: CORTEX_PLATFORM_URL
  platform_url: ""

upstream:
  # LLM provider base URL.
  # The proxy appends the incoming request path (/v1/chat/completions, etc.) to this.
  # CLI: --upstream-url=https://api.openai.com   env: CORTEX_UPSTREAM_URL
  base_url: "https://api.openai.com"

  # LLM API key (optional).
  # If set, this key replaces the Authorization header forwarded to the upstream.
  # If left empty, the agent's own Authorization header is passed through unchanged.
  # CLI: --upstream-key=sk-...   env: CORTEX_UPSTREAM_KEY
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
