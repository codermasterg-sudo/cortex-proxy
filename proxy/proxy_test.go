package proxy_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/proxy"
)

func TestInterceptReplaceBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"messages":        []map[string]string{{"role": "user", "content": "compressed"}},
			"tokens_before":   100,
			"tokens_after":    40,
			"record_id":       "rec-001",
			"has_ccr_markers": false,
		})
	}))
	defer srv.Close()

	client := platform.NewClient(srv.URL, "test-key", 3000)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)
	handler := proxy.NewHandler(client, cfgMgr, "")

	body := map[string]any{
		"model":    "claude-3-5-sonnet-20241022",
		"messages": []map[string]string{{"role": "user", "content": "original long content"}},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	newBody, recordID, err := handler.InterceptRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if recordID != "rec-001" {
		t.Errorf("expected rec-001, got %s", recordID)
	}

	var result map[string]any
	json.Unmarshal(newBody, &result)
	msgs := result["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	if content != "compressed" {
		t.Errorf("expected compressed body, got %s", content)
	}
}

func TestInterceptFallbackOnPlatformError(t *testing.T) {
	// 平台不可达时，原样透传
	client := platform.NewClient("http://127.0.0.1:1", "key", 100)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)
	handler := proxy.NewHandler(client, cfgMgr, "")

	original := map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hello"}}}
	bodyBytes, _ := json.Marshal(original)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	newBody, recordID, _ := handler.InterceptRequest(req)
	if recordID != "" {
		t.Error("expected empty recordID on error")
	}
	var result map[string]any
	json.Unmarshal(newBody, &result)
	if result["model"] != "gpt-4o" {
		t.Error("expected original body on fallback")
	}
}
