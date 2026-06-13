package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
)

// ExtractAndEnqueueUsage 从响应中提取上报字段并入队。
// - 普通 JSON 响应：直接解析，按 fields 提取
// - SSE 流式响应：实时分叉，不阻塞 agent 接收流，goroutine 聚合字段后触发上报
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
		tapSSEStream(resp, recordID, fields, rep)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	// 还原 body 供下游读取
	resp.Body = io.NopCloser(strings.NewReader(string(body)))

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	collected := collectFields(data, recordID, fields)
	if len(collected) > 1 {
		rep.Enqueue(collected)
	}
}

// tapSSEStream 将 SSE body 实时分叉：
//   - resp.Body 替换为透明的 teeReadCloser，goproxy 读取时同时写入 pipe
//   - goroutine 从 pipe 读取，逐行解析 data: 事件，聚合配置字段
//   - 遇到 [DONE] 或流结束时触发一次上报
//
// agent 接收流的延迟不受影响。
func tapSSEStream(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
) {
	pr, pw := io.Pipe()

	orig := resp.Body
	resp.Body = &teeReadCloser{
		r:    io.TeeReader(orig, pw),
		pw:   pw,
		orig: orig,
	}

	go func() {
		defer pr.Close()

		// 聚合：从所有事件中收集目标字段，后出现的值覆盖前面的
		// 这样可以跨多个 SSE 事件聚合（如 Anthropic 的 input tokens 在 message_start，
		// output tokens 在 message_delta）
		collected := map[string]any{"record_id": recordID}

		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 128*1024), 128*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				break
			}
			if payload == "" {
				continue
			}

			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				// 非 JSON 或不完整 chunk，跳过继续扫描
				continue
			}

			// 按 fields 配置从当前事件提取字段，合并到 collected
			mergeFields(collected, event, fields)
		}

		// [DONE] 或流自然结束后，只要有至少一个目标字段就上报
		if len(collected) > 1 {
			rep.Enqueue(collected)
		}
	}()
}

// teeReadCloser 包装原始 body：Read 时同步写入 pipe，Close 时关闭 pipe 通知消费 goroutine。
type teeReadCloser struct {
	r    io.Reader      // TeeReader(orig, pw)
	pw   *io.PipeWriter // 写端，Close 时关闭以通知消费方 EOF
	orig io.ReadCloser  // 原始 body
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	return t.r.Read(p)
}

func (t *teeReadCloser) Close() error {
	t.pw.Close() // 让 goroutine 的 scanner 收到 EOF
	return t.orig.Close()
}

// mergeFields 从 event（及其 usage 子对象）中提取 fields 配置的字段，写入 dst。
// 后调用的值覆盖先前的——跨多个 SSE 事件聚合时，越晚的事件越权威。
func mergeFields(dst map[string]any, event map[string]any, fields []string) {
	nested, _ := event["usage"].(map[string]any)
	for _, field := range fields {
		if v, ok := event[field]; ok {
			dst[field] = v
		} else if nested != nil {
			if v, ok := nested[field]; ok {
				dst[field] = v
			}
		}
	}
}

// collectFields 从单个 JSON 响应中按 fields 提取，返回含 record_id 的 map。
func collectFields(data map[string]any, recordID string, fields []string) map[string]any {
	result := map[string]any{"record_id": recordID}
	mergeFields(result, data, fields)
	return result
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
