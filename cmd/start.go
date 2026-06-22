package cmd

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cortex-io/cortex-proxy/config"
	"github.com/cortex-io/cortex-proxy/instance"
	"github.com/cortex-io/cortex-proxy/logger"
	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/proxy"
	"github.com/cortex-io/cortex-proxy/reporter"
)

func RunStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: cortex-proxy start [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Start the proxy. All flags are optional if the config file is set.")
		fmt.Fprintln(os.Stderr, "Priority (highest to lowest): CLI flag > environment variable > config file > built-in default.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "Config file: %s\n", config.DefaultPath())
		fmt.Fprintln(os.Stderr, "Run 'cortex-proxy install' to create a template config file.")
	}

	// Listening
	host := fs.String("host", "", "Listen `host/IP` (overrides config listen.host; default: 0.0.0.0 / all interfaces)\n\tenv: (none)")
	port := fs.Int("port", 0, "Listen `port` (overrides config listen.port; default: 7898)\n\tenv: (none)")

	// Cortex platform
	apiKey := fs.String("api-key", "", "Cortex platform API `key` (overrides config cortex.api_key)\n\tenv: CORTEX_API_KEY")
	platformURL := fs.String("platform", "", "Cortex platform `URL` (overrides config cortex.platform_url; default: https://api.cortex.io)\n\tenv: CORTEX_PLATFORM_URL")

	// Upstream LLM
	upstreamURL := fs.String("upstream-url", "", "LLM upstream base `URL` (overrides config upstream.base_url)\n\tenv: CORTEX_UPSTREAM_URL")
	upstreamKey := fs.String("upstream-key", "", "LLM upstream API `key` (overrides config upstream.api_key; leave empty to pass through agent's Authorization header)\n\tenv: CORTEX_UPSTREAM_KEY")

	// Other
	configPath := fs.String("config", config.DefaultPath(), "Config file `path`")
	debug := fs.Bool("debug", false, "Enable debug logging\n\tenv: CORTEX_DEBUG=1")

	fs.Parse(args)

	// Init file-based logging as early as possible.
	// Logs go to <binary-dir>/logs/cortex-proxy-YYYY-MM-DD.log and stderr.
	if binPath, err := os.Executable(); err == nil {
		logDir := filepath.Join(filepath.Dir(binPath), "logs")
		if cleanup, err := logger.Init(logDir); err != nil {
			log.Printf("[WARN]  failed to init log file (%v), logging to stderr only", err)
		} else {
			defer cleanup()
		}
	}

	// Enable debug as early as possible.
	if *debug || os.Getenv("CORTEX_DEBUG") == "1" {
		proxy.EnableDebug()
		log.Printf("[INFO]  debug logging enabled")
	}

	// Load config file (no error if file doesn't exist — flags/env take precedence).
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
		os.Exit(1)
	}

	// Resolve listen host: flag > config > default (empty = all interfaces)
	listenHost := cfg.Listen.Host
	if *host != "" {
		listenHost = *host
	}

	// Resolve listen port: flag > config > default 7898
	listenPort := cfg.Listen.Port
	if *port != 0 {
		listenPort = *port
	}
	if listenPort == 0 {
		listenPort = 7898
	}

	// Resolve Cortex API key: flag > env > config
	resolvedAPIKey := *apiKey
	if resolvedAPIKey == "" {
		resolvedAPIKey = os.Getenv("CORTEX_API_KEY")
	}
	if resolvedAPIKey == "" {
		resolvedAPIKey = cfg.Cortex.APIKey
	}

	// Resolve platform URL: flag > env > config > default
	resolvedPlatform := *platformURL
	if resolvedPlatform == "" {
		resolvedPlatform = os.Getenv("CORTEX_PLATFORM_URL")
	}
	if resolvedPlatform == "" {
		resolvedPlatform = cfg.Cortex.PlatformURL
	}
	if resolvedPlatform == "" {
		resolvedPlatform = "https://api.cortex.io"
	}

	// Resolve upstream: flag > env > config
	upstream := cfg.Upstream
	if u := *upstreamURL; u != "" {
		upstream.BaseURL = u
	} else if u := os.Getenv("CORTEX_UPSTREAM_URL"); u != "" {
		upstream.BaseURL = u
	}
	if k := *upstreamKey; k != "" {
		upstream.APIKey = k
	} else if k := os.Getenv("CORTEX_UPSTREAM_KEY"); k != "" {
		upstream.APIKey = k
	}

	if upstream.BaseURL == "" {
		fmt.Fprintln(os.Stderr, "Error: upstream URL is required.")
		fmt.Fprintln(os.Stderr, "Set it via (in order of precedence):")
		fmt.Fprintln(os.Stderr, "  --upstream-url=https://api.openai.com")
		fmt.Fprintln(os.Stderr, "  CORTEX_UPSTREAM_URL=https://api.openai.com")
		fmt.Fprintf(os.Stderr, "  upstream.base_url in %s\n", *configPath)
		fmt.Fprintln(os.Stderr, "Run 'cortex-proxy install' to create a template config file.")
		os.Exit(1)
	}

	if resolvedAPIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Cortex API key is required.")
		fmt.Fprintln(os.Stderr, "Set it via (in order of precedence):")
		fmt.Fprintln(os.Stderr, "  --api-key=ctxp_sk_...")
		fmt.Fprintln(os.Stderr, "  CORTEX_API_KEY=ctxp_sk_...")
		fmt.Fprintf(os.Stderr, "  cortex.api_key in %s\n", *configPath)
		os.Exit(1)
	}

	const defaultTimeoutMS = 3000
	const defaultBatchSize = 10
	const defaultFlushInterval = 5 * time.Second

	instanceID := instance.LoadOrCreate()
	addr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	log.Printf("[INFO]  starting cortex-proxy (instance=%s)", instanceID)
	log.Printf("[INFO]  platform=%s  listen=%s", resolvedPlatform, addr)
	log.Printf("[INFO]  upstream=%s", upstream.BaseURL)
	if upstream.APIKey != "" {
		log.Printf("[INFO]  upstream key: from config (agent Authorization header will be replaced)")
	} else {
		log.Printf("[INFO]  upstream key: pass-through agent Authorization header")
	}

	client := platform.NewClient(resolvedPlatform, resolvedAPIKey, defaultTimeoutMS)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)

	ctx := context.Background()
	if err := cfgMgr.SyncRefresh(ctx); err != nil {
		log.Printf("[WARN]  failed to fetch initial platform config: %v (using defaults)", err)
	} else {
		log.Printf("[INFO]  platform config loaded OK")
	}

	rep := reporter.New(resolvedPlatform, resolvedAPIKey, defaultBatchSize, defaultFlushInterval)

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

	log.Printf("[INFO]  cortex-proxy listening on %s (instance=%s)", addr, instanceID[:8])
	if err := http.ListenAndServe(addr, server); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
