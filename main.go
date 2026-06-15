package main

import (
	"fmt"
	"os"

	"github.com/cortex-io/cortex-proxy/cmd"
	"github.com/cortex-io/cortex-proxy/config"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "install":
		cmd.RunInstall(os.Args[2:])
	case "start":
		cmd.RunStart(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("cortex-proxy %s\n", version)
	fmt.Println()
	fmt.Println("A local HTTP reverse proxy that compresses LLM requests via the Cortex platform.")
	fmt.Println("Point your agent at http://localhost:7898 instead of the LLM provider directly.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cortex-proxy <command> [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  install     Create a template config file (run once before first start)")
	fmt.Println("  start       Start the proxy")
	fmt.Println("  help        Show this help message")
	fmt.Println("  version     Print version")
	fmt.Println()
	fmt.Println("Quick start:")
	fmt.Println("  cortex-proxy install                        # create config file")
	fmt.Println("  # edit config file: set upstream.base_url and cortex.api_key")
	fmt.Println("  cortex-proxy start                          # start with config file")
	fmt.Println()
	fmt.Println("Or without a config file:")
	fmt.Println("  cortex-proxy start \\")
	fmt.Println("    --api-key=ctxp_sk_... \\")
	fmt.Println("    --upstream-url=https://api.openai.com")
	fmt.Println()
	fmt.Println("Agent setup (after proxy is running):")
	fmt.Println("  OPENAI_BASE_URL=http://localhost:7898")
	fmt.Println("  OPENAI_API_KEY=<your-llm-key>  # pass-through if upstream.api_key is not set")
	fmt.Println()
	fmt.Printf("Config file: %s\n", config.DefaultPath())
	fmt.Println()
	fmt.Println("For per-command flags, run:")
	fmt.Println("  cortex-proxy start --help")
}
