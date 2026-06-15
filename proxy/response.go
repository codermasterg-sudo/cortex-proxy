package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
)

// ExtractAndEnqueueUsage 从响应中提取上报字段并入队。
// - 普通 JSON 响应：直接解析，按 fields 提取
// - SSE 流式响应：非阻塞旁路，不影响 agent 接收流速度
// usage 数据允许丢失，失败时只记日志不阻断主流。
func ExtractAndEnqueueUsage(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
	ttfbMs int,
	startTime time.Time,
) {
	if recordID == "" {
		return
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		tapSSEStream(resp, recordID, fields, rep, ttfbMs, startTime)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("cortex-proxy: read JSON response body error: %v", err)
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	logDebug("[JSON] extracting usage record=%s", recordID)
	collected := collectFields(data, recordID, fields)
	collected["ttfb_ms"] = ttfbMs
	collected["total_latency_ms"] = int(time.Since(startTime).Milliseconds())
	logDebug("[JSON] collected %d fields record=%s payload=%v", len(collected), recordID, collected)
	if len(collected) > 1 {
		logDebug("[JSON] enqueuing usage record=%s", recordID)
		rep.Enqueue(collected)
	}
}

// tapSSEStream 将 SSE body 替换为 sseReadCloser，主流读取时同步将每行拷贝到缓冲 channel。
// 独立 goroutine 从 channel 消费行、解析 usage 字段并上报；channel 带缓冲，消费慢时写端
// 立即丢弃溢出（usage 允许丢失），绝不阻塞主流读取速度。
//
// 参考 one-api 的同步旁路方案：主流与旁路复用同一次 Read，通过带缓冲 channel 解耦。
func tapSSEStream(
	resp *http.Response,
	recordID string,
	fields []string,
	rep *reporter.Reporter,
	ttfbMs int,
	startTime time.Time,
) {
	logDebug("[SSE] tapSSEStream enter record=%s", recordID)

	// 64 行缓冲足以覆盖正常 SSE 流的突发写入；消费方落后时丢弃而非阻塞。
	lineCh := make(chan string, 64)

	orig := resp.Body
	resp.Body = &sseReadCloser{orig: orig, lineCh: lineCh}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("cortex-proxy: SSE consumer panic: %v", r)
			}
		}()

		logDebug("[SSE] consumer goroutine started record=%s", recordID)
		collected := map[string]any{"record_id": recordID}

		for line := range lineCh {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" || payload == "" {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			before := len(collected)
			mergeFields(collected, event, fields)
			if len(collected) > before {
				logDebug("[SSE] mergeFields added %d fields from event record=%s", len(collected)-before, recordID)
			}
		}

		collected["ttfb_ms"] = ttfbMs
		collected["total_latency_ms"] = int(time.Since(startTime).Milliseconds())
		logDebug("[SSE] consumer goroutine finished record=%s fields=%d", recordID, len(collected))
		if len(collected) > 1 {
			logDebug("[SSE] enqueuing usage record=%s", recordID)
			rep.Enqueue(collected)
		}
	}()
}

// sseReadCloser 包装原始 SSE body。每次 Read 时直接读入调用方的 p，
// 同步扫描完整行，非阻塞地发到 lineCh；channel 满则静默丢弃（usage 允许丢失）。
// Close 时关闭 lineCh，通知消费 goroutine 退出。
type sseReadCloser struct {
	orig   io.ReadCloser
	lineCh chan string
	buf    bytes.Buffer // 跨 Read 拼接不完整行
}

func (s *sseReadCloser) Read(p []byte) (int, error) {
	n, err := s.orig.Read(p)
	if n > 0 {
		s.scanLines(p[:n])
	}
	return n, err
}

// scanLines 按行扫描字节切片，将完整行（含跨 Read 拼接的行）非阻塞发到 lineCh。
func (s *sseReadCloser) scanLines(data []byte) {
	s.buf.Write(data)
	for {
		b := s.buf.Bytes()
		idx := bytes.IndexByte(b, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(b[:idx]), "\r")
		s.buf.Next(idx + 1)
		select {
		case s.lineCh <- line:
		default: // channel 满，丢弃此行（usage 允许丢失）
		}
	}
}

func (s *sseReadCloser) Close() error {
	logDebug("[SSE] sseReadCloser.Close() called, closing lineCh")
	err := s.orig.Close()
	close(s.lineCh) // 通知消费 goroutine 退出
	return err
}

// mergeFields 从 event（及其 usage 子对象）中提取 fields 配置的字段，写入 dst。
// 后调用的值覆盖先前的——跨多个 SSE 事件聚合时，越晚的事件越权威。
// 同时始终将完整的 usage 子对象写入 dst["usage"]，供服务端做字段名适配。
func mergeFields(dst map[string]any, event map[string]any, fields []string) {
	nested, _ := event["usage"].(map[string]any)
	// 始终透传完整 usage 对象，服务端可自行处理字段名差异（如 prompt_cache_hit_tokens）
	if nested != nil {
		dst["usage"] = nested
		logDebug("[mergeFields] usage sub-object has %d fields", len(nested))
	}
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
