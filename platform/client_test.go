package platform_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cortex-io/cortex-proxy/platform"
)

func TestCompress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compress" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"messages":        []map[string]string{{"role": "user", "content": "compressed"}},
			"tokens_before":   100,
			"tokens_after":    50,
			"record_id":       "uuid-123",
			"has_ccr_markers": false,
		})
	}))
	defer srv.Close()

	c := platform.NewClient(srv.URL, "test-key", 3000)
	result, err := c.Compress(context.Background(), []byte(`{"messages":[],"model":"gpt-4o"}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.TokensBefore != 100 {
		t.Errorf("expected 100, got %d", result.TokensBefore)
	}
}

func TestCompressPassesInstanceIDHeader(t *testing.T) {
	var gotInstanceID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotInstanceID = r.Header.Get("X-Proxy-Instance-ID")
		json.NewEncoder(w).Encode(map[string]any{
			"messages":        []map[string]string{},
			"tokens_before":   0,
			"tokens_after":    0,
			"record_id":       "00000000-0000-0000-0000-000000000001",
			"has_ccr_markers": false,
		})
	}))
	defer srv.Close()

	c := platform.NewClient(srv.URL, "test-key", 3000)
	c.Compress(context.Background(), []byte(`{"messages":[],"model":"gpt-4o"}`), "", "inst-abc")
	if gotInstanceID != "inst-abc" {
		t.Errorf("expected X-Proxy-Instance-ID=inst-abc, got %q", gotInstanceID)
	}
}

func TestGetConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"compression": map[string]any{"enabled": true, "timeout_ms": 3000},
			"reporting": map[string]any{
				"fields":            []string{"input_tokens", "output_tokens"},
				"batch_size":        10,
				"flush_interval_ms": 5000,
			},
		})
	}))
	defer srv.Close()

	c := platform.NewClient(srv.URL, "test-key", 3000)
	cfg, err := c.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Compression.Enabled {
		t.Error("expected compression.enabled = true")
	}
}
