package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cortex-io/cortex-proxy/platform"
)

type Handler struct {
	client     *platform.Client
	configMgr  *platform.ConfigManager
	instanceID string
}

func NewHandler(client *platform.Client, configMgr *platform.ConfigManager, instanceID string) *Handler {
	return &Handler{client: client, configMgr: configMgr, instanceID: instanceID}
}

// InterceptRequest 拦截请求 body，调平台压缩，返回替换后的 body 和 record_id。
// 如果平台不可用或压缩禁用，返回原始 body。
// 安全保证：只把 request body（messages/model 等）发给 /v1/compress，不传原始 Authorization header。
func (h *Handler) InterceptRequest(req *http.Request) (newBody []byte, recordID string, err error) {
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(rawBody))

	cfg := h.configMgr.Get()
	if cfg != nil && !cfg.Compression.Enabled {
		logDebug("[SKIP] compression disabled by config, passthrough %s %s", req.Method, req.Host)
		return rawBody, "", nil
	}

	// 只压缩 application/json（LLM API 请求），用 mime.ParseMediaType 剥离 charset 等参数
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		logDebug("[SKIP] non-JSON content-type (%q), passthrough %s %s", req.Header.Get("Content-Type"), req.Method, req.Host)
		return rawBody, "", nil
	}

	clientAgent := req.Header.Get("User-Agent")
	logDebug("[COMPRESS] calling platform for %s %s%s (%d bytes)", req.Method, req.Host, req.URL.Path, len(rawBody))

	start := time.Now()
	result, err := h.client.Compress(req.Context(), rawBody, clientAgent, h.instanceID)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		logWarn("[COMPRESS] platform error for %s %s: %v (passthrough, %.0fms)", req.Method, req.Host, err, float64(elapsed))
		return rawBody, "", nil
	}

	ratio := 0.0
	if result.TokensBefore > 0 {
		ratio = 1.0 - float64(result.TokensAfter)/float64(result.TokensBefore)
	}
	logInfo("[COMPRESS] %s: %d→%d tokens (saved=%.0f%%, %dms) record=%s",
		req.Host, result.TokensBefore, result.TokensAfter, ratio*100, elapsed, result.RecordID)

	// 重组 body：用 json.RawMessage 保留所有原始字段的字节表示，只替换 messages。
	// 避免 map[string]any 的 float64 中转导致大整数精度丢失或格式变化。
	var original map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &original); err != nil {
		return rawBody, "", nil
	}
	messagesBytes, err := json.Marshal(result.Messages)
	if err != nil {
		return rawBody, "", nil
	}
	original["messages"] = json.RawMessage(messagesBytes)
	newBodyBytes, err := json.Marshal(original)
	if err != nil {
		return rawBody, "", nil
	}
	return newBodyBytes, result.RecordID, nil
}
