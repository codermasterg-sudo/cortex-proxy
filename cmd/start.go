package cmd

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/proxy"
	"github.com/cortex-io/cortex-proxy/reporter"
)

func RunStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	apiKey := fs.String("api-key", os.Getenv("CORTEX_API_KEY"), "Cortex API key")
	platformURL := fs.String("platform", getEnvOr("CORTEX_PLATFORM_URL", "https://api.cortex.io"), "Platform URL")
	port := fs.Int("port", 7898, "Proxy listen port")
	fs.Parse(args)

	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: --api-key or CORTEX_API_KEY is required")
		os.Exit(1)
	}

	client := platform.NewClient(*platformURL, *apiKey, 3000)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)
	ctx := context.Background()
	go cfgMgr.Start(ctx)

	rep := reporter.New(*platformURL, *apiKey, 10, 5*time.Second)
	go rep.Start(ctx)

	proxyServer, err := proxy.NewProxyServer(client, cfgMgr, rep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("cortex-proxy listening on %s\n", addr)
	if err := http.ListenAndServe(addr, proxyServer); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
