package cmd

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cortex-io/cortex-proxy/config"
	"github.com/cortex-io/cortex-proxy/instance"
	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/proxy"
	"github.com/cortex-io/cortex-proxy/reporter"
)

func RunStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	apiKey := fs.String("api-key", os.Getenv("CORTEX_API_KEY"), "Cortex platform API key")
	platformURL := fs.String("platform", getEnvOr("CORTEX_PLATFORM_URL", "https://api.cortex.io"), "Platform URL")
	port := fs.Int("port", 7898, "Listen port")
	debug := fs.Bool("debug", os.Getenv("CORTEX_DEBUG") == "1", "Enable debug logging")
	configPath := fs.String("config", config.DefaultPath(), "Config file path")
	// CLI flags override config file values.
	upstreamURL := fs.String("upstream-url", os.Getenv("CORTEX_UPSTREAM_URL"), "LLM upstream base URL (overrides config file)")
	upstreamKey := fs.String("upstream-key", os.Getenv("CORTEX_UPSTREAM_KEY"), "LLM upstream API key (overrides config file; leave empty to pass through agent's Authorization header)")
	fs.Parse(args)

	if *debug {
		proxy.EnableDebug()
		log.Printf("[INFO]  debug logging enabled")
	}

	// Load config file (no error if file doesn't exist — flags/env take precedence).
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
		os.Exit(1)
	}

	// Merge: CLI flags / env vars override config file values.
	upstream := cfg.Upstream
	if *upstreamURL != "" {
		upstream.BaseURL = *upstreamURL
	}
	if *upstreamKey != "" {
		upstream.APIKey = *upstreamKey
	}

	// Cortex API key: CLI > env > config file
	if *apiKey == "" {
		*apiKey = cfg.Cortex.APIKey
	}
	// Platform URL: CLI > env > config file
	if *platformURL == getEnvOr("CORTEX_PLATFORM_URL", "https://api.cortex.io") && cfg.Cortex.PlatformURL != "" {
		*platformURL = cfg.Cortex.PlatformURL
	}

	if upstream.BaseURL == "" {
		fmt.Fprintln(os.Stderr, "Error: upstream URL is required.")
		fmt.Fprintln(os.Stderr, "Options (in order of precedence):")
		fmt.Fprintln(os.Stderr, "  --upstream-url=https://api.openai.com")
		fmt.Fprintln(os.Stderr, "  CORTEX_UPSTREAM_URL=https://api.openai.com")
		fmt.Fprintf(os.Stderr, "  upstream.base_url in %s\n", *configPath)
		fmt.Fprintln(os.Stderr, "Run 'cortex-proxy install' to create a template config file.")
		os.Exit(1)
	}

	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Cortex API key is required.")
		fmt.Fprintln(os.Stderr, "Options (in order of precedence):")
		fmt.Fprintln(os.Stderr, "  --api-key=ctxp_sk_...")
		fmt.Fprintln(os.Stderr, "  CORTEX_API_KEY=ctxp_sk_...")
		fmt.Fprintf(os.Stderr, "  cortex.api_key in %s\n", *configPath)
		os.Exit(1)
	}

	const defaultTimeoutMS = 3000
	const defaultBatchSize = 10
	const defaultFlushInterval = 5 * time.Second

	instanceID := instance.LoadOrCreate()
	log.Printf("[INFO]  starting cortex-proxy (instance=%s)", instanceID)
	log.Printf("[INFO]  platform=%s  port=%d", *platformURL, *port)
	log.Printf("[INFO]  upstream=%s", upstream.BaseURL)
	if upstream.APIKey != "" {
		log.Printf("[INFO]  upstream key: from config (agent Authorization header will be replaced)")
	} else {
		log.Printf("[INFO]  upstream key: pass-through agent Authorization header")
	}

	client := platform.NewClient(*platformURL, *apiKey, defaultTimeoutMS)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)

	ctx := context.Background()
	if err := cfgMgr.SyncRefresh(ctx); err != nil {
		log.Printf("[WARN]  failed to fetch initial platform config: %v (using defaults)", err)
	} else {
		log.Printf("[INFO]  platform config loaded OK")
	}

	rep := reporter.New(*platformURL, *apiKey, defaultBatchSize, defaultFlushInterval)

	cfgMgr.OnRefresh(func(pcfg *platform.ProxyConfig) {
		rep.UpdateConfig(pcfg.Reporting.BatchSize, pcfg.Reporting.FlushIntervalMS)
		client.UpdateCompressTimeout(pcfg.Compression.TimeoutMS)
		log.Printf("[INFO]  platform config refreshed: compressTimeout=%dms batchSize=%d flushInterval=%dms",
			pcfg.Compression.TimeoutMS, pcfg.Reporting.BatchSize, pcfg.Reporting.FlushIntervalMS)
	})

	if pcfg := cfgMgr.Get(); pcfg != nil {
		rep.UpdateConfig(pcfg.Reporting.BatchSize, pcfg.Reporting.FlushIntervalMS)
		client.UpdateCompressTimeout(pcfg.Compression.TimeoutMS)
	}

	go cfgMgr.Start(ctx)
	go rep.Start(ctx)

	server := proxy.NewServer(client, cfgMgr, rep, instanceID, upstream)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[INFO]  cortex-proxy listening on %s (instance=%s)", addr, instanceID[:8])
	if err := http.ListenAndServe(addr, server); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
