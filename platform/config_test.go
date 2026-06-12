package platform_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cortex-io/cortex-proxy/platform"
)

func TestConfigManagerRefreshes(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(platform.ProxyConfig{
			Compression: platform.CompressionConfig{Enabled: true, TimeoutMS: 3000},
			Reporting: platform.ReportingConfig{
				Fields: []string{"input_tokens"}, BatchSize: 10, FlushIntervalMS: 5000,
			},
		})
	}))
	defer srv.Close()

	c := platform.NewClient(srv.URL, "key", 3000)
	mgr := platform.NewConfigManager(c, 50*time.Millisecond) // 50ms 刷新间隔（测试用）
	go mgr.Start(context.Background())

	time.Sleep(120 * time.Millisecond)
	cfg := mgr.Get()
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 refreshes, got %d", callCount)
	}
}

func TestConfigManagerFallback(t *testing.T) {
	// 平台不可达时返回 nil（调用方应使用内置默认值）
	c := platform.NewClient("http://127.0.0.1:1", "key", 100)
	mgr := platform.NewConfigManager(c, time.Minute)
	cfg := mgr.Get()
	if cfg != nil {
		t.Error("expected nil config when never fetched")
	}
}
