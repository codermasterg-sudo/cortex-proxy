package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
)

// ExtractAndEnqueueUsage 从非流式响应中提取 usage 字段并入队
func ExtractAndEnqueueUsage(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
) {
	if recordID == "" {
		return
	}
	// SSE 流式响应：不读取，直接透传（兼容 "text/event-stream; charset=utf-8" 等变体）
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
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

	usage := map[string]any{"record_id": recordID}
	for _, field := range fields {
		if v, ok := data[field]; ok {
			usage[field] = v
		}
		// 嵌套在 usage 对象里（OpenAI 格式）
		if u, ok := data["usage"].(map[string]any); ok {
			if v, ok := u[field]; ok {
				usage[field] = v
			}
		}
	}
	if len(usage) > 1 { // 至少有 record_id 以外的字段
		rep.Enqueue(usage)
	}
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
