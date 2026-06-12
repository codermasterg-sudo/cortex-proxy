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

	// 内置默认值，平台配置加载后会通过回调覆盖
	const defaultTimeoutMS = 3000
	const defaultBatchSize = 10
	const defaultFlushInterval = 5 * time.Second

	client := platform.NewClient(*platformURL, *apiKey, defaultTimeoutMS)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)

	// 同步拉取首次配置（忽略错误，失败时使用内置默认值）
	ctx := context.Background()
	cfgMgr.SyncRefresh(ctx)

	rep := reporter.New(*platformURL, *apiKey, defaultBatchSize, defaultFlushInterval)

	// 注册回调：平台配置刷新时更新 reporter 的动态参数
	cfgMgr.OnRefresh(func(cfg *platform.ProxyConfig) {
		rep.UpdateConfig(cfg.Reporting.BatchSize, cfg.Reporting.FlushIntervalMS)
	})

	// 应用已加载的配置（如果首次同步成功）
	if cfg := cfgMgr.Get(); cfg != nil {
		rep.UpdateConfig(cfg.Reporting.BatchSize, cfg.Reporting.FlushIntervalMS)
	}

	go cfgMgr.Start(ctx)
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
