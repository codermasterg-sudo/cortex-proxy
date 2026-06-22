package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	// 原地字节替换：只替换原始 body 中 "messages" 字段的值，其余字节一个不动。
	// result.Messages 是 json.RawMessage，保留平台返回的原始字节序，不做二次 Marshal。
	// 即使 tokens 数量未变（如仅做了格式归一化），也统一走替换路径，
	// 保证每次请求的 messages 格式一致，维持 LLM KV cache 跨请求命中率。
	newBodyBytes, err := replaceMessagesField(rawBody, result.Messages)
	if err != nil {
		logWarn("[COMPRESS] replaceMessagesField failed: %v, passthrough", err)
		return rawBody, "", nil
	}
	return newBodyBytes, result.RecordID, nil
}

// replaceMessagesField 在原始 JSON bytes 中原地替换 "messages" 字段的值。
// 其余所有字节（字段顺序、空白符、数字格式）保持 byte-level 完全一致，
// 确保 LLM KV cache 对非 messages 部分的 prefix 命中不受影响。
func replaceMessagesField(original []byte, newMessages json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(original))

	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		afterKey := dec.InputOffset()

		var rawVal json.RawMessage
		if err := dec.Decode(&rawVal); err != nil {
			return nil, err
		}
		afterVal := dec.InputOffset()

		if key != "messages" {
			continue
		}

		// 从 afterKey 向前扫描，找到 ':' 后跳过空白，定位值的起始字节。
		i := afterKey
		for i < int64(len(original)) && original[i] != ':' {
			i++
		}
		i++ // skip ':'
		for i < int64(len(original)) && (original[i] == ' ' || original[i] == '\t' || original[i] == '\n' || original[i] == '\r') {
			i++
		}

		result := make([]byte, 0, len(original)+len(newMessages))
		result = append(result, original[:i]...)
		result = append(result, newMessages...)
		result = append(result, original[afterVal:]...)
		return result, nil
	}

	return nil, fmt.Errorf(`"messages" field not found`)
}
