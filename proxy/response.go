package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
)

// ExtractAndEnqueueUsage 从响应中提取 usage 字段并入队。
// - 普通 JSON 响应：直接解析
// - SSE 流式响应：逐行扫描 data: 事件，从最后一个含 usage 字段的 JSON 对象提取
func ExtractAndEnqueueUsage(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
) {
	if recordID == "" {
		return
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		extractFromSSE(resp, recordID, fields, rep)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	usage := extractUsageFields(data, recordID, fields)
	if len(usage) > 1 {
		rep.Enqueue(usage)
	}
}

// extractFromSSE 读取 SSE 流，将所有 data: 行收集，再找含 usage 字段的最后一条 JSON 事件。
// 读完后把完整 body 还原到 resp.Body，供 goproxy 透传给调用方。
func extractFromSSE(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
) {
	// 用 TeeReader 同时读取 + 保留原始字节，保证 resp.Body 可被下游读取
	var buf bytes.Buffer
	tee := io.TeeReader(resp.Body, &buf)

	var lastUsage map[string]any

	scanner := bufio.NewScanner(tee)
	// SSE 行最长 64KB（包含大 JSON 对象时可能更长，保守使用 128KB）
	scanner.Buffer(make([]byte, 128*1024), 128*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// 非 JSON 或不完整 chunk，跳过
			continue
		}

		// 检查顶层或嵌套 usage 对象是否含有目标字段
		if containsUsageFields(event, fields) {
			lastUsage = event
		}
	}
	// scanner 读完后 buf 中已有完整数据
	resp.Body = io.NopCloser(&buf)

	if lastUsage != nil {
		usage := extractUsageFields(lastUsage, recordID, fields)
		if len(usage) > 1 {
			rep.Enqueue(usage)
		}
	}
}

// containsUsageFields 检查 data map 中（顶层或 usage 子对象）是否有任何目标字段。
func containsUsageFields(data map[string]any, fields []string) bool {
	nested, _ := data["usage"].(map[string]any)
	for _, field := range fields {
		if _, ok := data[field]; ok {
			return true
		}
		if nested != nil {
			if _, ok := nested[field]; ok {
				return true
			}
		}
	}
	return false
}

// extractUsageFields 从 data 中按 fields 列表提取值，优先顶层再查 usage 嵌套对象。
func extractUsageFields(data map[string]any, recordID string, fields []string) map[string]any {
	usage := map[string]any{"record_id": recordID}
	nested, _ := data["usage"].(map[string]any)
	for _, field := range fields {
		if v, ok := data[field]; ok {
			usage[field] = v
		} else if nested != nil {
			if v, ok := nested[field]; ok {
				usage[field] = v
			}
		}
	}
	return usage
}

// DefaultReportingFields 当配置未加载时的兜底字段列表
var DefaultReportingFields = []string{
	"input_tokens", "prompt_tokens",
	"output_tokens", "completion_tokens",
	"cache_read_tokens", "cache_write_tokens",
	"stop_reason",
}

func GetReportingFields(cfg *platform.ProxyConfig) []string {
	if cfg != nil && len(cfg.Reporting.Fields) > 0 {
		return cfg.Reporting.Fields
	}
	return DefaultReportingFields
}
