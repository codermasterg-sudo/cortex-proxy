package proxy

import (
	"encoding/json"
	"testing"
)

// TestReplaceMessagesFieldPreservesOrder 确认替换后原始字段顺序和空白符保持不变，
// 避免 JSON 重序列化破坏 LLM KV cache 的 byte-level prefix 一致性。
func TestReplaceMessagesFieldPreservesOrder(t *testing.T) {
	// 字段顺序：stream → model → messages（非字母序，模拟真实 SDK 请求）
	original := []byte(`{"stream":true,"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`)
	newMsgs, _ := json.Marshal([]map[string]string{{"role": "user", "content": "compressed"}})

	got, err := replaceMessagesField(original, newMsgs)
	if err != nil {
		t.Fatal(err)
	}

	// 前缀（stream, model 及 "messages": 之前的所有字节）必须 byte-identical
	prefix := `{"stream":true,"model":"gpt-4o","messages":`
	if string(got[:len(prefix)]) != prefix {
		t.Errorf("prefix changed:\n  want: %s\n  got:  %s", prefix, string(got[:len(prefix)]))
	}

	// 后缀（,"max_tokens":100}）必须 byte-identical
	suffix := `,"max_tokens":100}`
	if string(got[len(got)-len(suffix):]) != suffix {
		t.Errorf("suffix changed:\n  want: %s\n  got:  %s", suffix, string(got[len(got)-len(suffix):]))
	}

	// messages 值已替换
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msgs := result["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	if content != "compressed" {
		t.Errorf("messages not replaced: got %s", content)
	}
}

func TestReplaceMessagesFieldWithWhitespace(t *testing.T) {
	// 带缩进的 JSON，空白符必须保留
	original := []byte("{\n  \"model\": \"claude-3-opus\",\n  \"messages\": [{\"role\": \"user\", \"content\": \"original\"}]\n}")
	newMsgs, _ := json.Marshal([]map[string]string{{"role": "user", "content": "new"}})

	got, err := replaceMessagesField(original, newMsgs)
	if err != nil {
		t.Fatal(err)
	}

	// 前缀（换行、缩进）不变
	prefix := "{\n  \"model\": \"claude-3-opus\",\n  \"messages\": "
	if string(got[:len(prefix)]) != prefix {
		t.Errorf("whitespace not preserved:\n  want: %q\n  got:  %q", prefix, string(got[:len(prefix)]))
	}

	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msgs := result["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "new" {
		t.Error("messages not replaced")
	}
}

func TestReplaceMessagesFieldNotFound(t *testing.T) {
	_, err := replaceMessagesField([]byte(`{"model":"gpt-4"}`), []byte(`[]`))
	if err == nil {
		t.Error("expected error when messages field missing")
	}
}
