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

// TestInterceptNoCompression 验证 tokens 未变化时仍然做 messages 替换，
// 保证格式一致性（如平台做了空格归一化），维持跨请求 KV cache 命中率。
func TestInterceptNoCompression(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 平台返回格式归一化后的 messages（去掉了空格），tokens 数量不变
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"messages":[{"role":"user","content":"hello"}],"tokens_before":42,"tokens_after":42,"record_id":"rec-noop","has_ccr_markers":false}`))
	}))
	defer srv.Close()

	client := platform.NewClient(srv.URL, "test-key", 3000)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)
	handler := proxy.NewHandler(client, cfgMgr, "")

	// rawBody 带空格（模拟 SDK 请求）
	rawBody := []byte(`{"model":"gpt-4o","messages":[{"role": "user", "content": "hello"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")

	newBody, recordID, _ := handler.InterceptRequest(req)

	// messages 应被平台返回的紧凑格式替换（保证后续请求格式一致）
	if bytes.Contains(newBody, []byte(`"role": "user"`)) {
		t.Error("messages should be replaced with platform output (compact), not original with spaces")
	}
	if recordID != "rec-noop" {
		t.Errorf("expected rec-noop, got %s", recordID)
	}
	// 外层结构保留原始字节（model、stream 等不变）
	if !bytes.Contains(newBody, []byte(`"model":"gpt-4o"`)) {
		t.Error("outer JSON structure should be preserved")
	}
}

// TestInterceptPreservesMessageKeyOrder 验证实际压缩时 messages key 顺序来自平台原始字节，
// 不被 Go map 字母序覆盖。
func TestInterceptPreservesMessageKeyOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 平台返回的 messages 里 role 在 content 前（非字母序），模拟 Python dict 自然顺序
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"messages":[{"role":"user","content":"compressed"}],"tokens_before":100,"tokens_after":40,"record_id":"rec-order","has_ccr_markers":false}`))
	}))
	defer srv.Close()

	client := platform.NewClient(srv.URL, "test-key", 3000)
	cfgMgr := platform.NewConfigManager(client, 5*time.Minute)
	handler := proxy.NewHandler(client, cfgMgr, "")

	rawBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"original long text"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")

	newBody, _, _ := handler.InterceptRequest(req)

	// messages 字段值必须是平台原样返回的字节，role 在前
	expected := `[{"role":"user","content":"compressed"}]`
	if !bytes.Contains(newBody, []byte(expected)) {
		t.Errorf("key order from platform not preserved\n  want messages: %s\n  got body: %s", expected, newBody)
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
